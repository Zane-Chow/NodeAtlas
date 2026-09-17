package events

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

type fixedSubscriber struct{ events chan Event }

func (subscriber fixedSubscriber) Subscribe(context.Context) <-chan Event { return subscriber.events }

func TestHTTPStreamsTypedJSONEvents(t *testing.T) {
	stream := make(chan Event, 1)
	stream <- Event{ID: 17, Type: "operation.updated", Data: json.RawMessage(`{"operation_id":"operation-a"}`)}
	close(stream)
	handler := NewHTTPHandler(fixedSubscriber{events: stream}, HTTPOptions{})
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/events", nil))

	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "text/event-stream", response.Header().Get("Content-Type"))
	require.Equal(t, "no-cache", response.Header().Get("Cache-Control"))
	require.Contains(t, response.Body.String(), "retry: 3000\n")
	require.Contains(t, response.Body.String(), "id: 17\n")
	require.Contains(t, response.Body.String(), "event: operation.updated\n")
	require.Contains(t, response.Body.String(), `data: {"operation_id":"operation-a"}`)
}
