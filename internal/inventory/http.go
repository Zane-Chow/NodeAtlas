package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type SyncRequester interface {
	RequestSync(context.Context, string) error
}

type HTTPHandler struct {
	repository    Repository
	syncRequester SyncRequester
}

func NewHTTPHandler(repository Repository, syncRequester SyncRequester) http.Handler {
	handler := &HTTPHandler{repository: repository, syncRequester: syncRequester}
	router := chi.NewRouter()
	router.Get("/servers", handler.list)
	router.Route("/servers/{serverID}", func(server chi.Router) {
		server.Get("/", handler.get)
		server.Post("/refresh", handler.refresh)
	})
	return router
}

func (handler *HTTPHandler) list(response http.ResponseWriter, request *http.Request) {
	state := State(strings.TrimSpace(request.URL.Query().Get("state")))
	if state != "" && !validState(state) {
		writeError(response, http.StatusBadRequest, "invalid_filter", "Server state filter is invalid")
		return
	}
	servers, err := handler.repository.List(request.Context(), Filter{
		ConnectionID: strings.TrimSpace(request.URL.Query().Get("connection_id")),
		ProviderType: strings.TrimSpace(request.URL.Query().Get("provider_type")),
		State:        state,
		Query:        strings.TrimSpace(request.URL.Query().Get("q")),
	})
	if err != nil {
		writeError(response, http.StatusInternalServerError, "internal_error", "Unable to list servers")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"servers": servers, "total": len(servers)})
}

func (handler *HTTPHandler) get(response http.ResponseWriter, request *http.Request) {
	server, err := handler.repository.FindByID(request.Context(), chi.URLParam(request, "serverID"))
	if errors.Is(err, ErrNotFound) {
		writeError(response, http.StatusNotFound, "server_not_found", "Server was not found")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "internal_error", "Unable to load server")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"server": server})
}

func (handler *HTTPHandler) refresh(response http.ResponseWriter, request *http.Request) {
	server, err := handler.repository.FindByID(request.Context(), chi.URLParam(request, "serverID"))
	if errors.Is(err, ErrNotFound) {
		writeError(response, http.StatusNotFound, "server_not_found", "Server was not found")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "internal_error", "Unable to load server")
		return
	}
	if err := handler.syncRequester.RequestSync(request.Context(), server.ConnectionID); err != nil {
		writeError(response, http.StatusInternalServerError, "sync_failed", "Unable to queue server refresh")
		return
	}
	writeJSON(response, http.StatusAccepted, map[string]string{"status": "queued"})
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func writeError(response http.ResponseWriter, status int, code, message string) {
	writeJSON(response, status, map[string]any{"error": map[string]string{
		"code": code, "message": message, "request_id": uuid.NewString(),
	}})
}
