package console

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

func TestWebSocketGatewayConsumesTicketAndRunsMockConsole(t *testing.T) {
	ticket := "one-use-websocket-ticket"
	hash := sha256.Sum256([]byte(ticket))
	now := time.Date(2026, 9, 18, 15, 0, 0, 0, time.UTC)
	repository := &gatewayRepository{session: Session{ID: "session-a", ServerID: "server-a", TicketHash: hash[:], ExpiresAt: now.Add(time.Minute), Result: ResultPending}}
	targets := NewMemoryTargetStore()
	targets.Put("session-a", mustGatewayURL(t, "mock+ws://console/session-a"))
	gateway := NewWebSocketGateway(repository, targets, nil, GatewayOptions{Now: func() time.Time { return now }})
	server := httptest.NewServer(gateway)
	t.Cleanup(server.Close)

	address := "ws" + strings.TrimPrefix(server.URL, "http") + "/console/" + ticket
	connection, _, err := websocket.Dial(context.Background(), address, nil)
	require.NoError(t, err)
	_, banner, err := connection.Read(context.Background())
	require.NoError(t, err)
	require.Contains(t, string(banner), "Mock Console")
	require.NoError(t, connection.Write(context.Background(), websocket.MessageText, []byte("status")))
	_, echoed, err := connection.Read(context.Background())
	require.NoError(t, err)
	require.Equal(t, "status", string(echoed))
	require.NoError(t, connection.Write(context.Background(), websocket.MessageBinary, []byte{0, 1, 2, 255}))
	messageType, echoedBinary, err := connection.Read(context.Background())
	require.NoError(t, err)
	require.Equal(t, websocket.MessageBinary, messageType)
	require.Equal(t, []byte{0, 1, 2, 255}, echoedBinary)
	require.NoError(t, connection.Close(websocket.StatusNormalClosure, "done"))

	_, response, err := websocket.Dial(context.Background(), address, nil)
	require.Error(t, err)
	require.NotNil(t, response)
	require.Equal(t, 401, response.StatusCode)
	require.Eventually(t, func() bool { return repository.result() == ResultCompleted }, time.Second, 10*time.Millisecond)
}

func TestWebSocketGatewayProxiesRawVNCBinaryFrames(t *testing.T) {
	proxySide, vncSide := net.Pipe()
	t.Cleanup(func() { _ = vncSide.Close() })
	fixture := newRawGatewayFixture(t, proxySide)
	connection := fixture.Dial(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	want := []byte{0, 1, 2, 255}
	go func() { _, _ = vncSide.Write(want) }()
	messageType, got, err := connection.Read(ctx)
	require.NoError(t, err)
	require.Equal(t, websocket.MessageBinary, messageType)
	require.Equal(t, want, got)
	require.NoError(t, connection.Write(ctx, websocket.MessageBinary, want))
	require.NoError(t, vncSide.SetReadDeadline(time.Now().Add(time.Second)))
	buffer := make([]byte, len(want))
	_, err = io.ReadFull(vncSide, buffer)
	require.NoError(t, err)
	require.Equal(t, want, buffer)
	_, ok := fixture.Targets.Take("session-a")
	require.False(t, ok)
	_, response, err := websocket.Dial(ctx, fixture.Address, nil)
	require.Error(t, err)
	require.NotNil(t, response)
	require.Equal(t, 401, response.StatusCode)
	require.NoError(t, connection.Close(websocket.StatusNormalClosure, "done"))
	require.Eventually(t, func() bool { return fixture.Repository.result() == ResultCompleted }, time.Second, 10*time.Millisecond)
	_ = vncSide.SetWriteDeadline(time.Now().Add(time.Second))
	_, err = vncSide.Write([]byte{1})
	require.ErrorIs(t, err, io.ErrClosedPipe)
}

func TestWebSocketGatewayRejectsTextForRawVNC(t *testing.T) {
	proxySide, vncSide := net.Pipe()
	t.Cleanup(func() { _ = vncSide.Close() })
	fixture := newRawGatewayFixture(t, proxySide)
	connection := fixture.Dial(t)
	require.NoError(t, connection.Write(context.Background(), websocket.MessageText, []byte("not-rfb")))
	go func() { _, _, _ = connection.Read(context.Background()) }()
	require.Eventually(t, func() bool { return fixture.Repository.result() == ResultFailed }, time.Second, 10*time.Millisecond)
	_ = vncSide.SetWriteDeadline(time.Now().Add(time.Second))
	_, err := vncSide.Write([]byte{1})
	require.ErrorIs(t, err, io.ErrClosedPipe)
}

func TestWebSocketGatewayRawVNCCancellationClosesTCP(t *testing.T) {
	for _, kind := range []string{"idle", "absolute", "blocked TCP write"} {
		t.Run(kind, func(t *testing.T) {
			proxySide, vncSide := net.Pipe()
			t.Cleanup(func() { _ = vncSide.Close() })
			fixture := newRawGatewayFixture(t, proxySide, func(options *GatewayOptions) {
				options.IdleTimeout = time.Second
				options.AbsoluteTimeout = time.Second
				if kind == "idle" {
					options.IdleTimeout = 50 * time.Millisecond
				} else {
					options.AbsoluteTimeout = 50 * time.Millisecond
				}
			})
			connection := fixture.Dial(t)
			if kind == "blocked TCP write" {
				require.NoError(t, connection.Write(context.Background(), websocket.MessageBinary, []byte{1}))
			}
			go func() { _, _, _ = connection.Read(context.Background()) }()
			require.Eventually(t, func() bool { return fixture.Repository.result() == ResultFailed }, time.Second, 10*time.Millisecond)
			_ = vncSide.SetWriteDeadline(time.Now().Add(time.Second))
			_, err := vncSide.Write([]byte{1})
			require.ErrorIs(t, err, io.ErrClosedPipe)
		})
	}
}

func TestWebSocketGatewayRawVNCRejectsOversizeAndUpstreamEOF(t *testing.T) {
	for _, kind := range []string{"oversize", "EOF"} {
		t.Run(kind, func(t *testing.T) {
			proxySide, vncSide := net.Pipe()
			t.Cleanup(func() { _ = vncSide.Close() })
			fixture := newRawGatewayFixture(t, proxySide)
			connection := fixture.Dial(t)
			if kind == "oversize" {
				_ = connection.Write(context.Background(), websocket.MessageBinary, make([]byte, 1024*1024+1))
			} else {
				require.NoError(t, vncSide.Close())
			}
			go func() { _, _, _ = connection.Read(context.Background()) }()
			require.Eventually(t, func() bool { return fixture.Repository.result() == ResultFailed }, time.Second, 10*time.Millisecond)
		})
	}
}

type rawGatewayFixture struct {
	Repository *gatewayRepository
	Targets    *MemoryTargetStore
	Address    string
}

func (fixture rawGatewayFixture) Dial(t *testing.T) *websocket.Conn {
	t.Helper()
	connection, _, err := websocket.Dial(context.Background(), fixture.Address, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = connection.CloseNow() })
	return connection
}

func newRawGatewayFixture(t *testing.T, upstream net.Conn, configure ...func(*GatewayOptions)) rawGatewayFixture {
	t.Helper()
	t.Cleanup(func() { _ = upstream.Close() })
	ticket := "one-use-raw-vnc-ticket"
	hash := sha256.Sum256([]byte(ticket))
	repository := &gatewayRepository{session: Session{ID: "session-a", ServerID: "server-a", TicketHash: hash[:], ExpiresAt: time.Now().Add(time.Minute), Result: ResultPending}}
	targets := NewMemoryTargetStore()
	targets.Put("session-a", mustGatewayURL(t, "vnc+tcp://vnc.example.test:5951"))
	options := GatewayOptions{TargetPolicy: NewTargetPolicy(staticResolver{"vnc.example.test": {net.ParseIP("203.0.113.10")}}, TargetPolicyOptions{
		Dialer: tcpDialerFunc(func(_ context.Context, network, address string) (net.Conn, error) {
			if network != "tcp" || address != "203.0.113.10:5951" {
				return nil, ErrTargetRejected
			}
			return upstream, nil
		}),
	})}
	for _, configure := range configure {
		configure(&options)
	}
	server := httptest.NewServer(NewWebSocketGateway(repository, targets, nil, options))
	t.Cleanup(server.Close)
	return rawGatewayFixture{Repository: repository, Targets: targets, Address: "ws" + strings.TrimPrefix(server.URL, "http") + "/console/" + ticket}
}

func TestWebSocketGatewayRejectsUnknownTicket(t *testing.T) {
	gateway := NewWebSocketGateway(&gatewayRepository{}, NewMemoryTargetStore(), nil, GatewayOptions{})
	server := httptest.NewServer(gateway)
	t.Cleanup(server.Close)
	_, response, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(server.URL, "http")+"/console/unknown", nil)
	require.Error(t, err)
	require.NotNil(t, response)
	require.Equal(t, 401, response.StatusCode)
}

func TestWebSocketGatewayWSSPinsPolicyDialAndKeepsPanelTLSHostname(t *testing.T) {
	var requests atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		require.Equal(t, "example.com", r.TLS.ServerName)
		require.Equal(t, "/vnc", r.URL.Path)
		require.Equal(t, "203.0.113.20:5901/d9428888-122b-11e1-b85c-61cd3cbb3210", r.URL.Query().Get("url"))
		require.Empty(t, r.Header.Get("Authorization"))
		connection, err := websocket.Accept(w, r, nil)
		require.NoError(t, err)
		defer connection.CloseNow()
		kind, payload, err := connection.Read(r.Context())
		if err == nil {
			_ = connection.Write(r.Context(), kind, payload)
		}
		_, _, _ = connection.Read(r.Context())
	}))
	t.Cleanup(upstream.Close)
	pool := x509.NewCertPool()
	pool.AddCert(upstream.Certificate())
	for _, tc := range []struct {
		host    string
		trusted bool
	}{{"example.com", true}, {"wrong.example.test", true}, {"example.com", false}} {
		t.Run(tc.host+"/trusted="+strconv.FormatBool(tc.trusted), func(t *testing.T) {
			host := tc.host
			var dials atomic.Int32
			var resolutions atomic.Int32
			var roots *x509.CertPool
			if tc.trusted {
				roots = pool
			}
			policy := NewTargetPolicy(resolverFunc(func(context.Context, string) ([]net.IP, error) {
				if resolutions.Add(1) == 1 {
					return []net.IP{net.ParseIP("203.0.113.9")}, nil
				}
				return []net.IP{net.ParseIP("203.0.113.10")}, nil
			}), TargetPolicyOptions{
				RootCAs: roots,
				Dialer: tcpDialerFunc(func(ctx context.Context, network, address string) (net.Conn, error) {
					dials.Add(1)
					require.Equal(t, "203.0.113.10:443", address)
					return (&net.Dialer{}).DialContext(ctx, network, upstream.Listener.Addr().String())
				}),
			})
			fixture := newWSSGatewayFixture(t, "wss://"+host+"/vnc?url=203.0.113.20%3A5901%2Fd9428888-122b-11e1-b85c-61cd3cbb3210", policy)
			connection := fixture.Dial(t)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if host == "example.com" && tc.trusted {
				want := []byte{0, 1, 2, 255}
				require.NoError(t, connection.Write(ctx, websocket.MessageBinary, want))
				kind, payload, err := connection.Read(ctx)
				require.NoError(t, err)
				require.Equal(t, websocket.MessageBinary, kind)
				require.Equal(t, want, payload)
				require.NoError(t, connection.Close(websocket.StatusNormalClosure, "done"))
				require.Eventually(t, func() bool { return fixture.Repository.result() == ResultCompleted }, time.Second, time.Millisecond)
			} else {
				_, _, _ = connection.Read(ctx)
				require.Eventually(t, func() bool { return fixture.Repository.result() == ResultFailed }, time.Second, time.Millisecond)
			}
			require.EqualValues(t, 1, dials.Load())
			require.EqualValues(t, 2, resolutions.Load())
			_, exists := fixture.Targets.Take("session-a")
			require.False(t, exists)
		})
	}
	require.EqualValues(t, 1, requests.Load(), "invalid TLS hostname must never reach the HTTP handler")
}

func TestWebSocketGatewayWSSRechecksDNSAfterTicketAndRejectsMixedAnswers(t *testing.T) {
	for _, blocked := range []string{"127.0.0.1", "169.254.169.254", "10.0.0.1", "::1"} {
		t.Run(blocked, func(t *testing.T) {
			var lookups atomic.Int32
			policy := NewTargetPolicy(resolverFunc(func(context.Context, string) ([]net.IP, error) {
				if lookups.Add(1) <= 2 {
					return []net.IP{net.ParseIP("203.0.113.10")}, nil
				}
				return []net.IP{net.ParseIP("203.0.113.10"), net.ParseIP(blocked)}, nil
			}), TargetPolicyOptions{Dialer: tcpDialerFunc(func(context.Context, string, string) (net.Conn, error) {
				t.Error("WSS must not dial a rebinding DNS answer set")
				return nil, ErrTargetRejected
			})})
			target := "wss://panel.example.test/vnc?url=203.0.113.20%3A5901%2Fuuid"
			require.NoError(t, policy.ValidateEmbedded(context.Background(), mustGatewayURL(t, target)))
			fixture := newWSSGatewayFixture(t, target, policy)
			connection := fixture.Dial(t)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, _, _ = connection.Read(ctx)
			require.Eventually(t, func() bool { return fixture.Repository.result() == ResultFailed }, time.Second, time.Millisecond)
			require.EqualValues(t, 3, lookups.Load())
		})
	}
}

func TestWebSocketGatewayWSSRejectsRedirectAndMissingPolicy(t *testing.T) {
	var requests atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Redirect(w, r, "https://untrusted.invalid/secret", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(upstream.Close)
	pool := x509.NewCertPool()
	pool.AddCert(upstream.Certificate())
	policy := NewTargetPolicy(staticResolver{"example.com": {net.ParseIP("203.0.113.10")}}, TargetPolicyOptions{
		RootCAs: pool, Dialer: tcpDialerFunc(func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, upstream.Listener.Addr().String())
		}),
	})
	for _, policy := range []*TargetPolicy{policy, nil} {
		fixture := newWSSGatewayFixture(t, "wss://example.com/vnc", policy)
		connection := fixture.Dial(t)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, _, _ = connection.Read(ctx)
		cancel()
		require.Eventually(t, func() bool { return fixture.Repository.result() == ResultFailed }, time.Second, time.Millisecond)
	}
	require.EqualValues(t, 1, requests.Load())
}

func newWSSGatewayFixture(t *testing.T, target string, policy *TargetPolicy) rawGatewayFixture {
	t.Helper()
	ticket := "one-use-wss-ticket"
	hash := sha256.Sum256([]byte(ticket))
	repository := &gatewayRepository{session: Session{ID: "session-a", ServerID: "server-a", TicketHash: hash[:], ExpiresAt: time.Now().Add(time.Minute), Result: ResultPending}}
	targets := NewMemoryTargetStore()
	targets.Put("session-a", mustGatewayURL(t, target))
	server := httptest.NewServer(NewWebSocketGateway(repository, targets, nil, GatewayOptions{TargetPolicy: policy}))
	t.Cleanup(server.Close)
	return rawGatewayFixture{Repository: repository, Targets: targets, Address: "ws" + strings.TrimPrefix(server.URL, "http") + "/console/" + ticket}
}

type gatewayRepository struct {
	mu           sync.Mutex
	session      Session
	consumed     bool
	closedResult Result
}

func (repository *gatewayRepository) Create(context.Context, Session) error { return nil }
func (repository *gatewayRepository) ConsumeTicket(_ context.Context, hash []byte, openedAt time.Time) (Session, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.consumed || repository.session.ID == "" || !bytes.Equal(hash, repository.session.TicketHash) || !openedAt.Before(repository.session.ExpiresAt) {
		return Session{}, ErrTicketUnavailable
	}
	repository.consumed = true
	repository.session.OpenedAt = &openedAt
	return repository.session, nil
}
func (repository *gatewayRepository) FindByID(context.Context, string) (Session, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return repository.session, nil
}
func (repository *gatewayRepository) Close(_ context.Context, _ string, result Result, _ time.Time) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.closedResult = result
	return nil
}

func (repository *gatewayRepository) result() Result {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return repository.closedResult
}

func mustGatewayURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	target, err := url.Parse(raw)
	require.NoError(t, err)
	return target
}
