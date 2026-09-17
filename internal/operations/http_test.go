package operations

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"controlpanel/internal/inventory"
	"controlpanel/internal/providers"
	"github.com/stretchr/testify/require"
)

func TestHTTPQueuesAndReplaysIdempotentPowerAction(t *testing.T) {
	service, repository, _, queue, _ := newOperationServiceFixture(t, inventory.StateStopped, providers.Capabilities{
		CanStart: providers.Capability{Available: true},
	})
	handler := NewHTTPHandler(service, repository)
	request := httptest.NewRequest(http.MethodPost, "/servers/server-a/actions/start", nil)
	request.Header.Set("Idempotency-Key", "idem-http")
	request.Header.Set("X-Request-ID", "request-http")
	request.RemoteAddr = "192.0.2.44:1234"
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	require.Equal(t, http.StatusAccepted, response.Code)
	require.Contains(t, response.Body.String(), `"status":"queued"`)
	require.NotContains(t, response.Body.String(), "idem-http")
	replay := httptest.NewRecorder()
	handler.ServeHTTP(replay, request.Clone(request.Context()))
	require.Equal(t, http.StatusOK, replay.Code)
	require.Equal(t, []string{"operation-a"}, queue.operationIDs)
}

func TestHTTPListsAndFindsOperations(t *testing.T) {
	service, repository, _, _, _ := newOperationServiceFixture(t, inventory.StateRunning, providers.Capabilities{
		CanStop: providers.Capability{Available: true},
	})
	_, _, err := service.Request(t.Context(), Request{ServerID: "server-a", Action: ActionStop, IdempotencyKey: "idem-list"})
	require.NoError(t, err)
	handler := NewHTTPHandler(service, repository)

	listed := httptest.NewRecorder()
	handler.ServeHTTP(listed, httptest.NewRequest(http.MethodGet, "/operations?status=queued&server_id=server-a", nil))
	require.Equal(t, http.StatusOK, listed.Code)
	require.Contains(t, listed.Body.String(), `"id":"operation-a"`)
	detail := httptest.NewRecorder()
	handler.ServeHTTP(detail, httptest.NewRequest(http.MethodGet, "/operations/operation-a", nil))
	require.Equal(t, http.StatusOK, detail.Code)
}

func TestHTTPMapsInvalidActionAndStateConflict(t *testing.T) {
	service, repository, _, _, _ := newOperationServiceFixture(t, inventory.StateRunning, providers.Capabilities{
		CanStart: providers.Capability{Available: false},
	})
	handler := NewHTTPHandler(service, repository)

	invalid := httptest.NewRecorder()
	handler.ServeHTTP(invalid, httptest.NewRequest(http.MethodPost, "/servers/server-a/actions/destroy", nil))
	require.Equal(t, http.StatusBadRequest, invalid.Code)
	require.Contains(t, invalid.Body.String(), `"code":"invalid_action"`)
	conflict := httptest.NewRecorder()
	handler.ServeHTTP(conflict, httptest.NewRequest(http.MethodPost, "/servers/server-a/actions/start", nil))
	require.Equal(t, http.StatusConflict, conflict.Code)
	require.Contains(t, conflict.Body.String(), `"code":"state_conflict"`)
}
