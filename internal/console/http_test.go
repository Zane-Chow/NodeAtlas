package console

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type consoleAPI struct{}

func (consoleAPI) Options(context.Context, string) (ConsoleOptions, error) {
	return ConsoleOptions{Embedded: Option{Available: true}, Window: Option{Available: true}, Portal: Option{Available: true}, Order: []string{"embedded", "window", "portal"}}, nil
}
func (consoleAPI) CreateEmbedded(context.Context, string, string, string) (SessionTicket, error) {
	return SessionTicket{SessionID: "session-a", Ticket: "ticket-a", ExpiresAt: time.Date(2026, 9, 18, 15, 0, 0, 0, time.UTC)}, nil
}
func (consoleAPI) OpenWindow(context.Context, string, string, string) (ExternalTarget, error) {
	return ExternalTarget{URL: "https://console.example.test/window"}, nil
}
func (consoleAPI) ProviderPortal(context.Context, string, string, string) (ExternalTarget, error) {
	return ExternalTarget{URL: "https://portal.example.test/server"}, nil
}

func TestHTTPHandlerExposesOptionsSessionsWindowAndPortal(t *testing.T) {
	handler := NewHTTPHandler(consoleAPI{})
	options := httptest.NewRecorder()
	handler.ServeHTTP(options, httptest.NewRequest(http.MethodGet, "/servers/server-a/console-options", nil))
	require.Equal(t, http.StatusOK, options.Code)
	require.Contains(t, options.Body.String(), `"embedded":{"available":true}`)

	session := httptest.NewRecorder()
	handler.ServeHTTP(session, httptest.NewRequest(http.MethodPost, "/servers/server-a/console-sessions", bytes.NewBufferString(`{}`)))
	require.Equal(t, http.StatusCreated, session.Code)
	var sessionBody map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(session.Body.Bytes(), &sessionBody))
	require.Contains(t, string(sessionBody["session"]), `"ticket":"ticket-a"`)

	window := httptest.NewRecorder()
	handler.ServeHTTP(window, httptest.NewRequest(http.MethodPost, "/servers/server-a/console-window", nil))
	require.Equal(t, http.StatusOK, window.Code)
	require.Contains(t, window.Body.String(), "https://console.example.test/window")

	portal := httptest.NewRecorder()
	handler.ServeHTTP(portal, httptest.NewRequest(http.MethodGet, "/servers/server-a/provider-portal", nil))
	require.Equal(t, http.StatusOK, portal.Code)
	require.Contains(t, portal.Body.String(), "https://portal.example.test/server")
}
