package console

import (
	"bytes"
	"context"
	"crypto/sha256"
	"net/http/httptest"
	"net/url"
	"strings"
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
	require.True(t, repository.consumed)
	require.Eventually(t, func() bool { return repository.closedResult == ResultCompleted }, time.Second, 10*time.Millisecond)
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

type gatewayRepository struct {
	session      Session
	consumed     bool
	closedResult Result
}

func (repository *gatewayRepository) Create(context.Context, Session) error { return nil }
func (repository *gatewayRepository) ConsumeTicket(_ context.Context, hash []byte, openedAt time.Time) (Session, error) {
	if repository.consumed || repository.session.ID == "" || !bytes.Equal(hash, repository.session.TicketHash) || !openedAt.Before(repository.session.ExpiresAt) {
		return Session{}, ErrTicketUnavailable
	}
	repository.consumed = true
	repository.session.OpenedAt = &openedAt
	return repository.session, nil
}
func (repository *gatewayRepository) FindByID(context.Context, string) (Session, error) {
	return repository.session, nil
}
func (repository *gatewayRepository) Close(_ context.Context, _ string, result Result, _ time.Time) error {
	repository.closedResult = result
	return nil
}

func mustGatewayURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	target, err := url.Parse(raw)
	require.NoError(t, err)
	return target
}
