package events

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

type HTTPOptions struct {
	Heartbeat time.Duration
}

type HTTPHandler struct {
	subscriber Subscriber
	heartbeat  time.Duration
}

func NewHTTPHandler(subscriber Subscriber, options HTTPOptions) http.Handler {
	heartbeat := options.Heartbeat
	if heartbeat <= 0 {
		heartbeat = 15 * time.Second
	}
	handler := &HTTPHandler{subscriber: subscriber, heartbeat: heartbeat}
	router := chi.NewRouter()
	router.Get("/events", handler.stream)
	return router
}

func (handler *HTTPHandler) stream(response http.ResponseWriter, request *http.Request) {
	flusher, ok := response.(http.Flusher)
	if !ok {
		http.Error(response, "Streaming is unavailable", http.StatusInternalServerError)
		return
	}
	response.Header().Set("Content-Type", "text/event-stream")
	response.Header().Set("Cache-Control", "no-cache")
	response.Header().Set("X-Accel-Buffering", "no")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write([]byte("retry: 3000\n\n"))
	flusher.Flush()
	stream := handler.subscriber.Subscribe(request.Context())
	heartbeat := time.NewTicker(handler.heartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case event, open := <-stream:
			if !open {
				return
			}
			data := event.Data
			if !json.Valid(data) {
				data = json.RawMessage(`{}`)
			}
			var compact bytes.Buffer
			_ = json.Compact(&compact, data)
			_, _ = fmt.Fprintf(response, "id: %d\nevent: %s\ndata: %s\n\n", event.ID, event.Type, compact.Bytes())
			flusher.Flush()
		case <-heartbeat.C:
			_, _ = response.Write([]byte(": heartbeat\n\n"))
			flusher.Flush()
		}
	}
}
