package solusvm2

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"controlpanel/internal/providers"
	"controlpanel/internal/providers/contracttest"
	providernetwork "controlpanel/internal/providers/network"
	"github.com/stretchr/testify/require"
)

const fixtureToken = "fixture-solus-api-token"

func fixtureConfig(endpoint string) providers.ConnectionConfig {
	return providers.ConnectionConfig{ID: "connection-a", Type: "solusvm2", Endpoint: endpoint, Settings: json.RawMessage(`{}`), Credentials: json.RawMessage(`{"api_token":"` + fixtureToken + `"}`)}
}

func fixtureProvider(t *testing.T, handler http.HandlerFunc) *Provider {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+fixtureToken || strings.Contains(r.URL.String(), fixtureToken) {
			t.Error("authentication must use the Bearer header only")
		}
		require.Equal(t, "application/json", r.Header.Get("Accept"))
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	p, err := newFactory(factoryOptions{allowLoopback: true, rootCAs: pool}).Create(fixtureConfig(server.URL))
	require.NoError(t, err)
	t.Cleanup(p.(*Provider).client.CloseIdleConnections)
	return p.(*Provider)
}

func writeJSON(t *testing.T, w http.ResponseWriter, body any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	require.NoError(t, json.NewEncoder(w).Encode(body))
}

func fixtureServer(id int, status string) map[string]any {
	return map[string]any{
		"id": id, "name": fmt.Sprintf("server-%d", id), "project": map[string]any{"id": 7, "owner": "body-secret"},
		"status": status, "real_status": status, "virtualization_type": "kvm", "os_type": "Linux",
		"specifications": map[string]any{"vcpu": 2, "ram": 2147483648, "disk": 40},
		"settings":       map[string]any{"vnc_enabled": true, "vnc_password": "body-secret", "user": "body-secret"},
		"vnc_url":        "body-secret", "user": map[string]any{"email": "body-secret"},
		"ip_addresses": map[string]any{"ipv4": []any{map[string]any{"ip": "192.0.2.10", "issued_for": "body-secret"}}, "ipv6": []any{map[string]any{"primary_ip": "2001:0db8::1", "range": "2001:db8::/64"}}},
	}
}

func fixturePage(data any, page, last int) map[string]any {
	return map[string]any{"data": data, "meta": map[string]any{"current_page": page, "last_page": last}, "links": map[string]any{"next": "https://untrusted.invalid/body-secret"}}
}

func assertSafeError(t *testing.T, err error, code providers.ErrorCode) *providers.Error {
	t.Helper()
	require.Error(t, err)
	var failure *providers.Error
	require.ErrorAs(t, err, &failure)
	require.Equal(t, code, failure.Code)
	for _, secret := range []string{fixtureToken, "body-secret", "127.0.0.1", "Authorization"} {
		require.NotContains(t, err.Error(), secret)
		require.NotContains(t, failure.SafeMessage(), secret)
	}
	var urlError *url.Error
	require.False(t, errors.As(err, &urlError))
	return failure
}

type resolverFunc func(context.Context, string, string) ([]net.IP, error)

func (fn resolverFunc) LookupIP(ctx context.Context, network, host string) ([]net.IP, error) {
	return fn(ctx, network, host)
}

func publicResolver() resolverFunc {
	return func(context.Context, string, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("198.51.100.1")}, nil
	}
}

func TestFactoryStrictConfiguration(t *testing.T) {
	f := newFactory(factoryOptions{resolver: publicResolver()})
	for _, suffix := range []string{"", "/", "/api/v1", "/api/v1/"} {
		p, err := f.Create(fixtureConfig("https://panel.example:8443" + suffix))
		require.NoError(t, err)
		require.Equal(t, "https://panel.example:8443/api/v1", p.(*Provider).apiBase.String())
		portal, err := p.ProviderPortalURL(context.Background(), providers.ServerRef{})
		require.NoError(t, err)
		require.Equal(t, "https://panel.example:8443/", portal.String())
		portal.Path = "/modified"
		require.Equal(t, "/", p.(*Provider).panelURL.Path)
	}
	for _, endpoint := range []string{"http://panel.example", "https://user:pass@panel.example", "https://panel.example?", "https://panel.example#", "https://panel.example?token=body-secret", "https://panel.example/api/v2", "https://panel.example//api/v1", "https://panel.example/%61pi/v1", "https://panel.example/api/../api/v1", "https://panel.example:0", "https://panel.example:65536", "https://panel.example:", "https://panel.example:abc", "https://panel.example:0443", "https://[fe80::1%25eth0]"} {
		t.Run("endpoint", func(t *testing.T) {
			_, err := f.Create(fixtureConfig(endpoint))
			assertSafeError(t, err, providers.ErrorInvalidConfig)
		})
	}
	for _, raw := range []string{"null", "[]", `{"unknown":true}`, `{} {}`, `{"api_token":"body-secret"}`} {
		config := fixtureConfig("https://panel.example")
		config.Settings = json.RawMessage(raw)
		_, err := f.Create(config)
		assertSafeError(t, err, providers.ErrorInvalidConfig)
	}
	for _, raw := range []string{"null", "{}", "[]", `{"api_token":""}`, `{"api_token":"  "}`, `{"api_token":"abc\r\nx: secret"}`, `{"api_token":"valid","other":"body-secret"}`, `{"api_token":"valid"} {}`} {
		config := fixtureConfig("https://panel.example")
		config.Credentials = json.RawMessage(raw)
		_, err := f.Create(config)
		assertSafeError(t, err, providers.ErrorInvalidConfig)
	}
	config := fixtureConfig("https://panel.example")
	config.ID = ""
	_, err := f.Create(config)
	assertSafeError(t, err, providers.ErrorInvalidConfig)
}

func TestFactoryNetworkPolicy(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.1.2.3", "169.254.169.254", "::1", "fd00::1", "0.0.0.0"} {
		_, err := NewFactory(FactoryOptions{}).Create(fixtureConfig("https://" + net.JoinHostPort(ip, "443")))
		assertSafeError(t, err, providers.ErrorInvalidConfig)
	}
	_, cidr, err := net.ParseCIDR("10.1.2.0/24")
	require.NoError(t, err)
	_, err = NewFactory(FactoryOptions{AllowedPrivateCIDRs: []*net.IPNet{cidr}}).Create(fixtureConfig("https://10.1.2.3"))
	require.NoError(t, err)
	resolver := resolverFunc(func(context.Context, string, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("198.51.100.1"), net.ParseIP("10.1.2.3")}, nil
	})
	_, err = newFactory(factoryOptions{resolver: resolver}).Create(fixtureConfig("https://panel.example"))
	assertSafeError(t, err, providers.ErrorInvalidConfig)
	var resolutions atomic.Int32
	resolver = func(context.Context, string, string) ([]net.IP, error) {
		if resolutions.Add(1) == 1 {
			return []net.IP{net.ParseIP("198.51.100.1")}, nil
		}
		return []net.IP{net.ParseIP("127.0.0.1")}, nil
	}
	p, err := newFactory(factoryOptions{resolver: resolver}).Create(fixtureConfig("https://panel.example"))
	require.NoError(t, err)
	_, err = p.ValidateConnection(context.Background())
	assertSafeError(t, err, providers.ErrorNetwork)
	require.GreaterOrEqual(t, resolutions.Load(), int32(2))
	// Even a reused connection must revalidate DNS at a same-origin redirect.
	resolutions.Store(0)
	p, err = newFactory(factoryOptions{resolver: resolver}).Create(fixtureConfig("https://panel.example"))
	require.NoError(t, err)
	redirect, err := http.NewRequest(http.MethodGet, "https://panel.example/redirected", nil)
	require.NoError(t, err)
	require.Error(t, p.(*Provider).client.CheckRedirect(redirect, nil))
	require.EqualValues(t, 2, resolutions.Load())
}

func TestInventoryPaginationAndContract(t *testing.T) {
	p := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		switch r.URL.Path {
		case "/api/v1/servers":
			page := 1
			if r.URL.Query().Get("page") == "2" {
				page = 2
			} else {
				require.Equal(t, "1", r.URL.Query().Get("page"))
			}
			require.Len(t, r.URL.Query(), 1)
			writeJSON(t, w, fixturePage([]any{fixtureServer(page, "started")}, page, 2))
		case "/api/v1/servers/1":
			writeJSON(t, w, map[string]any{"data": fixtureServer(1, "started")})
		case "/api/v1/servers/2":
			writeJSON(t, w, map[string]any{"data": fixtureServer(2, "started")})
		default:
			w.WriteHeader(http.StatusUnauthorized)
		}
	})
	page, err := p.ListServers(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, "2", page.Next.Value)
	require.Len(t, page.Servers, 1)
	server := page.Servers[0]
	require.Equal(t, "7", server.Scope)
	require.JSONEq(t, `{"virt":"kvm","os_type":"Linux","cpu":2,"memory_mb":2048,"storage_gb":40}`, string(server.Spec))
	require.JSONEq(t, `[{"type":"ipv4","address":"192.0.2.10"},{"type":"ipv6","address":"2001:db8::1"}]`, string(server.Addresses))
	encoded, err := json.Marshal(server)
	require.NoError(t, err)
	for _, forbidden := range []string{"body-secret", "vnc_password", "vnc_url", "owner", fixtureToken} {
		require.NotContains(t, string(encoded), forbidden)
	}
	contracttest.Run(t, contracttest.Fixture{Provider: p, ErrorCases: []contracttest.ErrorCase{{Name: "authentication", Call: func(ctx context.Context) error {
		_, err := p.GetServer(ctx, providers.ServerRef{ExternalID: "99"})
		return err
	}, Forbidden: []string{fixtureToken, "body-secret"}}}})
}

func TestInventoryRejectsInvalidPagesAndIDs(t *testing.T) {
	for _, tc := range []struct {
		raw    string
		cursor *providers.Cursor
	}{
		{`{"data":[],"meta":{"current_page":1,"last_page":2}}`, nil},
		{`{"data":[],"meta":{"current_page":1,"last_page":0}}`, nil},
		{`{"data":[],"meta":{"current_page":1,"last_page":1000001}}`, nil},
		{`{"data":[],"meta":{"current_page":1,"last_page":3}}`, &providers.Cursor{Value: "2"}},
		{`{"data":null,"meta":{"current_page":1,"last_page":1}}`, nil},
		{`{"data":[{"id":0}],"meta":{"current_page":1,"last_page":1}}`, nil},
		{`{"data":[{"id":1},{"id":1}],"meta":{"current_page":1,"last_page":1}}`, nil},
	} {
		p := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) { writeJSON(t, w, json.RawMessage(tc.raw)) })
		_, err := p.ListServers(context.Background(), tc.cursor)
		assertSafeError(t, err, providers.ErrorProvider)
	}
	p := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) { t.Error("invalid identifiers must not cause a request") })
	for _, raw := range []string{"", "0", "-1", "+1", "01", " 1", "1.0", "1/2", "99999999999999999999", "1000001"} {
		_, err := p.ListServers(context.Background(), &providers.Cursor{Value: raw})
		assertSafeError(t, err, providers.ErrorInvalidConfig)
	}
	for _, raw := range []string{"", "0", "-1", "+1", "01", " 1", "1.0", "1/2"} {
		_, err := p.GetServer(context.Background(), providers.ServerRef{ExternalID: raw})
		assertSafeError(t, err, providers.ErrorNotFound)
		_, err = p.StartServer(context.Background(), providers.ServerRef{ExternalID: raw})
		assertSafeError(t, err, providers.ErrorNotFound)
	}
}

func TestInventoryStateAndCapabilities(t *testing.T) {
	for _, tc := range []struct {
		status, real          string
		processing, suspended bool
		want                  providers.ServerState
	}{
		{"started", "", false, false, providers.StateRunning}, {"started", "stopped", false, false, providers.StateStopped},
		{"stopped", "started", true, false, providers.StatePending}, {"processing", "started", false, false, providers.StatePending},
		{"started", "processing", false, false, providers.StatePending}, {"processing", "started", true, true, providers.StateSuspended},
		{"not exist", "", false, false, providers.StateError}, {"unavailable", "", false, false, providers.StateUnknown},
		{"body-secret", "body-secret", false, false, providers.StateUnknown},
	} {
		p := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) {
			s := fixtureServer(1, tc.status)
			s["real_status"] = tc.real
			s["is_processing"] = tc.processing
			s["is_suspended"] = tc.suspended
			writeJSON(t, w, map[string]any{"data": s})
		})
		s, err := p.GetServer(context.Background(), providers.ServerRef{ExternalID: "1"})
		require.NoError(t, err)
		require.Equal(t, tc.want, s.State)
		require.NotContains(t, s.RemoteState, "body-secret")
		require.Equal(t, tc.want == providers.StateStopped, s.Capabilities.CanStart.Available)
		require.Equal(t, tc.want == providers.StateRunning, s.Capabilities.CanStop.Available)
		require.Equal(t, tc.want == providers.StateRunning, s.Capabilities.CanReboot.Available)
		require.Equal(t, !tc.suspended, s.Capabilities.CanEmbedConsole.Available)
		require.True(t, s.Capabilities.HasProviderPortal.Available)
	}
}

func TestInventoryInvalidDetailAndLegacyAddresses(t *testing.T) {
	for _, mutation := range []func(map[string]any){
		func(s map[string]any) { s["id"] = 2 },
		func(s map[string]any) { s["specifications"] = map[string]any{"ram": -1} },
		func(s map[string]any) { s["project"] = map[string]any{"id": -1} },
		func(s map[string]any) {
			s["ip_addresses"] = map[string]any{"ipv4": []any{map[string]any{"ip": "body-secret"}}}
		},
		func(s map[string]any) {
			s["ip_addresses"] = map[string]any{"ipv6": []any{map[string]any{"primary_ip": "192.0.2.10"}}}
		},
	} {
		p := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) {
			s := fixtureServer(1, "started")
			mutation(s)
			writeJSON(t, w, map[string]any{"data": s})
		})
		_, err := p.GetServer(context.Background(), providers.ServerRef{ExternalID: "1"})
		assertSafeError(t, err, providers.ErrorProvider)
	}
	p := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) {
		s := fixtureServer(1, "stopped")
		delete(s, "ip_addresses")
		s["ips"] = []any{map[string]any{"ip": "192.0.2.10"}, map[string]any{"ip": "2001:db8::1"}}
		s["settings"] = map[string]any{"vnc_enabled": false}
		writeJSON(t, w, map[string]any{"data": s})
	})
	s, err := p.GetServer(context.Background(), providers.ServerRef{ExternalID: "1"})
	require.NoError(t, err)
	require.JSONEq(t, `[{"type":"ipv4","address":"192.0.2.10"},{"type":"ipv6","address":"2001:db8::1"}]`, string(s.Addresses))
	require.False(t, s.Capabilities.CanEmbedConsole.Available)
	require.False(t, s.Capabilities.CanOpenConsoleWindow.Available)
}

func TestPowerRequestsAndReceipts(t *testing.T) {
	for _, action := range []string{"start", "stop", "restart"} {
		t.Run(action, func(t *testing.T) {
			var calls atomic.Int32
			p := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, "/api/v1/servers/1/"+action, r.URL.Path)
				require.Empty(t, r.URL.RawQuery)
				raw, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				require.Equal(t, "application/json", r.Header.Get("Content-Type"))
				if action == "start" {
					require.Empty(t, raw)
				} else {
					require.JSONEq(t, `{"force":false}`, string(raw))
				}
				writeJSON(t, w, map[string]any{"data": map[string]any{"id": 987, "output": "body-secret"}})
			})
			call := map[string]func(context.Context, providers.ServerRef) (providers.ActionReceipt, error){"start": p.StartServer, "stop": p.StopServer, "restart": p.RebootServer}[action]
			receipt, err := call(context.Background(), providers.ServerRef{ExternalID: "1"})
			require.NoError(t, err)
			require.Equal(t, "987", receipt.RequestID)
			require.EqualValues(t, 1, calls.Load())
		})
	}
	for _, raw := range []string{`{}`, `{"data":null}`, `{"data":{"id":0}}`, `{"data":{"id":"body-secret"}}`} {
		p := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) { writeJSON(t, w, json.RawMessage(raw)) })
		_, err := p.StartServer(context.Background(), providers.ServerRef{ExternalID: "1"})
		assertSafeError(t, err, providers.ErrorProvider)
	}
}

func TestSafeHTTPFailures(t *testing.T) {
	for _, tc := range []struct {
		status int
		code   providers.ErrorCode
		retry  bool
	}{{401, providers.ErrorAuthentication, false}, {403, providers.ErrorPermission, false}, {404, providers.ErrorNotFound, false}, {409, providers.ErrorProvider, false}, {422, providers.ErrorProvider, false}, {429, providers.ErrorRateLimited, true}, {500, providers.ErrorProvider, true}, {503, providers.ErrorProvider, true}} {
		p := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte("body-secret " + fixtureToken))
		})
		_, err := p.ValidateConnection(context.Background())
		failure := assertSafeError(t, err, tc.code)
		require.Equal(t, tc.retry, failure.RetryableError())
	}
	for _, raw := range []string{"null", "[]", "{broken", "{} {}", strings.Repeat(" ", 4<<20) + `{}`} {
		p := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(raw))
		})
		_, err := p.ValidateConnection(context.Background())
		assertSafeError(t, err, providers.ErrorProvider)
	}
	p := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`{"data":[],"meta":{"current_page":1,"last_page":1}}`))
	})
	_, err := p.ValidateConnection(context.Background())
	assertSafeError(t, err, providers.ErrorProvider)
}

func TestRedirectTLSAndTimeoutSafety(t *testing.T) {
	p := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/servers" {
			http.Redirect(w, r, "/same-origin", http.StatusTemporaryRedirect)
			return
		}
		writeJSON(t, w, fixturePage([]any{}, 1, 1))
	})
	_, err := p.ValidateConnection(context.Background())
	require.NoError(t, err)
	for _, target := range []string{"https://other.example/body-secret", "http://127.0.0.1/body-secret", "https://user:pass@panel.example/body-secret", "/api/v1/servers"} {
		p := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target, http.StatusTemporaryRedirect)
		})
		_, err := p.ValidateConnection(context.Background())
		assertSafeError(t, err, providers.ErrorNetwork)
	}
	p = fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	p.client.Timeout = 20 * time.Millisecond
	_, err = p.ValidateConnection(context.Background())
	failure := assertSafeError(t, err, providers.ErrorNetwork)
	require.True(t, failure.RetryableError())
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("untrusted certificate must not reach handler") }))
	defer server.Close()
	untrusted, err := newFactory(factoryOptions{allowLoopback: true}).Create(fixtureConfig(server.URL))
	require.NoError(t, err)
	_, err = untrusted.ValidateConnection(context.Background())
	assertSafeError(t, err, providers.ErrorNetwork)
}

const fixtureVMUUID = "d9428888-122b-11e1-b85c-61cd3cbb3210"

func fixtureVNC() map[string]any {
	return map[string]any{
		"host": "203.0.113.20", "port": 5901,
		"vm": map[string]any{"id": 1, "uuid": fixtureVMUUID, "settings": map[string]any{"vnc_password": "temporary-vnc-password"}},
		// This upstream extension is deliberately untrusted and not a transport URL.
		"vnc_proxy_url": "wss://untrusted.invalid/body-secret", "extra": "body-secret",
	}
}

func consoleFixture(t *testing.T, serverData, vncData map[string]any) (*Provider, *atomic.Int32) {
	t.Helper()
	calls := &atomic.Int32{}
	p := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/servers/1":
			require.Equal(t, http.MethodGet, r.Method)
			writeJSON(t, w, map[string]any{"data": serverData})
		case "/api/v1/servers/1/vnc_up":
			calls.Add(1)
			require.Equal(t, http.MethodPost, r.Method)
			require.Equal(t, "application/json", r.Header.Get("Content-Type"))
			require.Empty(t, r.URL.RawQuery)
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			require.Empty(t, body)
			writeJSON(t, w, vncData)
		default:
			t.Errorf("unexpected console request path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	return p, calls
}

func TestOpenConsoleConstructsPanelWSSForBothModes(t *testing.T) {
	for _, mode := range []providers.ConsoleMode{providers.ConsoleEmbedded, providers.ConsoleWindow} {
		for _, host := range []string{"203.0.113.20", "2001:db8::20", "compute.example.test"} {
			t.Run(string(mode)+"/"+host, func(t *testing.T) {
				response := fixtureVNC()
				response["host"] = host
				p, calls := consoleFixture(t, fixtureServer(1, "started"), response)
				p.policy = providernetwork.NewPolicy(resolverFunc(func(_ context.Context, network, name string) ([]net.IP, error) {
					require.Equal(t, "ip", network)
					require.Equal(t, "compute.example.test", name)
					return []net.IP{net.ParseIP("203.0.113.21"), net.ParseIP("203.0.113.20")}, nil
				}), providernetwork.PolicyOptions{})
				target, err := p.OpenConsole(context.Background(), providers.ServerRef{ExternalID: "1"}, mode)
				require.NoError(t, err)
				require.EqualValues(t, 1, calls.Load())
				require.Equal(t, mode, target.Mode)
				require.Equal(t, "rfb", target.Protocol)
				require.Equal(t, "temporary-vnc-password", target.Password)
				require.Equal(t, "wss", target.URL.Scheme)
				require.Equal(t, p.panelURL.Host, target.URL.Host)
				require.Equal(t, "/vnc", target.URL.Path)
				require.Nil(t, target.URL.User)
				require.Empty(t, target.URL.Fragment)
				require.Len(t, target.URL.Query(), 1)
				if host == "compute.example.test" {
					host = "203.0.113.20" // Stable literal prevents management-node DNS rebinding.
				}
				require.Equal(t, net.JoinHostPort(host, "5901")+"/"+fixtureVMUUID, target.URL.Query().Get("url"))
				for _, secret := range []string{fixtureToken, "body-secret", "temporary-vnc-password", "compute.example.test", "untrusted.invalid"} {
					require.NotContains(t, target.URL.String(), secret)
				}
			})
		}
	}
}

func TestOpenConsoleValidatesCapabilityAndModeBeforeVNCRequest(t *testing.T) {
	for _, kind := range []string{"disabled", "suspended", "unsupported mode", "bad ID"} {
		t.Run(kind, func(t *testing.T) {
			server := fixtureServer(1, "started")
			mode, ref := providers.ConsoleEmbedded, providers.ServerRef{ExternalID: "1"}
			if kind == "disabled" {
				server["settings"] = map[string]any{"vnc_enabled": false}
			}
			if kind == "suspended" {
				server["is_suspended"] = true
			}
			if kind == "unsupported mode" {
				mode = "portal"
			}
			if kind == "bad ID" {
				ref.ExternalID = "01"
			}
			p, calls := consoleFixture(t, server, fixtureVNC())
			target, err := p.OpenConsole(context.Background(), ref, mode)
			code := providers.ErrorUnsupported
			if kind == "bad ID" {
				code = providers.ErrorNotFound
			}
			assertSafeError(t, err, code)
			require.Nil(t, target.URL)
			require.Empty(t, target.Password)
			require.Zero(t, calls.Load())
		})
	}
}

func TestOpenConsoleRejectsMalformedUpstreamData(t *testing.T) {
	mutations := map[string]func(map[string]any){
		"missing host":     func(v map[string]any) { delete(v, "host") },
		"missing VM":       func(v map[string]any) { delete(v, "vm") },
		"wrong ID":         func(v map[string]any) { v["vm"].(map[string]any)["id"] = 2 },
		"noncanonical ID":  func(v map[string]any) { v["vm"].(map[string]any)["id"] = "01" },
		"missing password": func(v map[string]any) { v["vm"].(map[string]any)["settings"] = map[string]any{} },
		"empty password":   func(v map[string]any) { v["vm"].(map[string]any)["settings"] = map[string]any{"vnc_password": ""} },
		"wrong envelope":   func(v map[string]any) { v["data"] = fixtureVNC(); delete(v, "host") },
	}
	for _, host := range []string{"", " body-secret", "body-secret ", "host/path", "host?token=body-secret", "user@host", "host#fragment", "host\\path", "host:5901", "[2001:db8::1]", "fe80::1%eth0", "host\nbody-secret", "host..test", "-host.test", "host-.test", "https://host", strings.Repeat("a", 64) + ".test"} {
		mutations["host/"+host] = func(v map[string]any) { v["host"] = host }
	}
	for _, port := range []any{nil, 0, -1, 65536, 1.5, "5901", "body-secret"} {
		mutations[fmt.Sprintf("port/%v", port)] = func(v map[string]any) { v["port"] = port }
	}
	for _, id := range []string{"", "body-secret", "d9428888122b11e1b85c61cd3cbb3210", strings.ToUpper(fixtureVMUUID), "00000000-0000-0000-0000-000000000000", fixtureVMUUID + "/path"} {
		mutations["UUID/"+id] = func(v map[string]any) { v["vm"].(map[string]any)["uuid"] = id }
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			response := fixtureVNC()
			mutate(response)
			p, _ := consoleFixture(t, fixtureServer(1, "started"), response)
			target, err := p.OpenConsole(context.Background(), providers.ServerRef{ExternalID: "1"}, providers.ConsoleEmbedded)
			assertSafeError(t, err, providers.ErrorProvider)
			require.Nil(t, target.URL)
			require.Empty(t, target.Password)
		})
	}
}

func TestOpenConsoleAppliesComputeNetworkPolicy(t *testing.T) {
	for _, tc := range []struct {
		host, cidr string
		addresses  []net.IP
		allowed    bool
	}{
		{host: "10.20.1.5"}, {host: "10.20.1.5", cidr: "10.20.0.0/16", allowed: true},
		{host: "fd00::5"}, {host: "fd00::5", cidr: "fd00::/64", allowed: true},
		{host: "127.0.0.1"}, {host: "169.254.169.254"}, {host: "0.0.0.0"}, {host: "::1"},
		{host: "compute.example.test", addresses: []net.IP{net.ParseIP("203.0.113.20"), net.ParseIP("10.20.1.5")}},
		{host: "compute.example.test"},
	} {
		t.Run(tc.host+tc.cidr, func(t *testing.T) {
			response := fixtureVNC()
			response["host"] = tc.host
			p, _ := consoleFixture(t, fixtureServer(1, "started"), response)
			var allowed []*net.IPNet
			if tc.cidr != "" {
				_, cidr, err := net.ParseCIDR(tc.cidr)
				require.NoError(t, err)
				allowed = []*net.IPNet{cidr}
			}
			p.policy = providernetwork.NewPolicy(resolverFunc(func(context.Context, string, string) ([]net.IP, error) { return tc.addresses, nil }), providernetwork.PolicyOptions{AllowedPrivateCIDRs: allowed})
			target, err := p.OpenConsole(context.Background(), providers.ServerRef{ExternalID: "1"}, providers.ConsoleEmbedded)
			if tc.allowed {
				require.NoError(t, err)
				require.Equal(t, net.JoinHostPort(tc.host, "5901")+"/"+fixtureVMUUID, target.URL.Query().Get("url"))
			} else {
				assertSafeError(t, err, providers.ErrorProvider)
				require.Nil(t, target.URL)
				require.NotContains(t, err.Error(), tc.host)
			}
		})
	}
}

func TestOpenConsoleRechecksComputeDNSForEachSession(t *testing.T) {
	response := fixtureVNC()
	response["host"] = "compute.example.test"
	p, _ := consoleFixture(t, fixtureServer(1, "started"), response)
	var resolutions atomic.Int32
	p.policy = providernetwork.NewPolicy(resolverFunc(func(context.Context, string, string) ([]net.IP, error) {
		if resolutions.Add(1) == 1 {
			return []net.IP{net.ParseIP("203.0.113.20")}, nil
		}
		return []net.IP{net.ParseIP("127.0.0.1")}, nil
	}), providernetwork.PolicyOptions{})
	first, err := p.OpenConsole(context.Background(), providers.ServerRef{ExternalID: "1"}, providers.ConsoleEmbedded)
	require.NoError(t, err)
	require.Contains(t, first.URL.Query().Get("url"), "203.0.113.20:5901")
	second, err := p.OpenConsole(context.Background(), providers.ServerRef{ExternalID: "1"}, providers.ConsoleWindow)
	assertSafeError(t, err, providers.ErrorProvider)
	require.Nil(t, second.URL)
	require.EqualValues(t, 2, resolutions.Load())
}

func TestOpenConsoleSanitizesVNCRequestErrors(t *testing.T) {
	for _, tc := range []struct {
		status int
		code   providers.ErrorCode
	}{
		{401, providers.ErrorAuthentication}, {403, providers.ErrorPermission},
		{404, providers.ErrorNotFound}, {422, providers.ErrorProvider}, {429, providers.ErrorRateLimited}, {500, providers.ErrorProvider},
	} {
		t.Run(strconv.Itoa(tc.status), func(t *testing.T) {
			p := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					writeJSON(t, w, map[string]any{"data": fixtureServer(1, "started")})
					return
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte("body-secret " + fixtureToken + " temporary-vnc-password 203.0.113.20:5901"))
			})
			target, err := p.OpenConsole(context.Background(), providers.ServerRef{ExternalID: "1"}, providers.ConsoleEmbedded)
			assertSafeError(t, err, tc.code)
			for _, secret := range []string{"temporary-vnc-password", "203.0.113.20", "5901"} {
				require.NotContains(t, err.Error(), secret)
			}
			require.Nil(t, target.URL)
			require.Empty(t, target.Password)
		})
	}
}
