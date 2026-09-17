package operations

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"

	"controlpanel/internal/inventory"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type OperationRequester interface {
	Request(context.Context, Request) (Operation, bool, error)
}

type HTTPHandler struct {
	service    OperationRequester
	repository Repository
}

func NewHTTPHandler(service OperationRequester, repository Repository) http.Handler {
	handler := &HTTPHandler{service: service, repository: repository}
	router := chi.NewRouter()
	router.Post("/servers/{serverID}/actions/{action}", handler.requestAction)
	router.Get("/operations", handler.list)
	router.Get("/operations/{operationID}", handler.get)
	return router
}

func (handler *HTTPHandler) requestAction(response http.ResponseWriter, request *http.Request) {
	idempotencyKey := strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	if len(idempotencyKey) > 255 {
		writeError(response, http.StatusBadRequest, "invalid_idempotency_key", "Idempotency key is too long")
		return
	}
	requestID := strings.TrimSpace(request.Header.Get("X-Request-ID"))
	if requestID == "" || len(requestID) > 255 {
		requestID = uuid.NewString()
	}
	operation, created, err := handler.service.Request(request.Context(), Request{
		ServerID: chi.URLParam(request, "serverID"), Action: Action(chi.URLParam(request, "action")),
		IdempotencyKey: idempotencyKey, RequestID: requestID, SourceIP: sourceIP(request.RemoteAddr),
	})
	if err != nil {
		writeOperationError(response, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusAccepted
	}
	writeJSON(response, status, map[string]any{"operation": operation})
}

func (handler *HTTPHandler) list(response http.ResponseWriter, request *http.Request) {
	status := Status(strings.TrimSpace(request.URL.Query().Get("status")))
	if status != "" && !validStatus(status) {
		writeError(response, http.StatusBadRequest, "invalid_filter", "Operation status filter is invalid")
		return
	}
	operations, err := handler.repository.List(request.Context(), Filter{
		ServerID: strings.TrimSpace(request.URL.Query().Get("server_id")), ConnectionID: strings.TrimSpace(request.URL.Query().Get("connection_id")), Status: status,
	})
	if err != nil {
		writeError(response, http.StatusInternalServerError, "internal_error", "Unable to list operations")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"operations": operations, "total": len(operations)})
}

func (handler *HTTPHandler) get(response http.ResponseWriter, request *http.Request) {
	operation, err := handler.repository.FindByID(request.Context(), chi.URLParam(request, "operationID"))
	if err != nil {
		writeOperationError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"operation": operation})
}

func writeOperationError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidAction):
		writeError(response, http.StatusBadRequest, "invalid_action", "Power action is invalid")
	case errors.Is(err, inventory.ErrNotFound):
		writeError(response, http.StatusNotFound, "server_not_found", "Server was not found")
	case errors.Is(err, ErrNotFound):
		writeError(response, http.StatusNotFound, "operation_not_found", "Operation was not found")
	case errors.Is(err, ErrStateConflict):
		writeError(response, http.StatusConflict, "state_conflict", "Server state does not allow this action")
	case errors.Is(err, ErrCapabilityUnavailable):
		writeError(response, http.StatusConflict, "capability_unavailable", "Provider does not allow this action")
	case errors.Is(err, ErrActiveOperation):
		writeError(response, http.StatusConflict, "active_operation", "Server already has an active operation")
	default:
		writeError(response, http.StatusInternalServerError, "internal_error", "Unable to process power operation")
	}
}

func validStatus(status Status) bool {
	switch status {
	case StatusQueued, StatusRunning, StatusVerifying, StatusSucceeded, StatusFailed, StatusTimedOut, StatusCancelled:
		return true
	default:
		return false
	}
}

func sourceIP(remoteAddress string) string {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err == nil {
		return host
	}
	return remoteAddress
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
