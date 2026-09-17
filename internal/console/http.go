package console

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

type API interface {
	Options(context.Context, string) (ConsoleOptions, error)
	CreateEmbedded(context.Context, string, string, string) (SessionTicket, error)
	OpenWindow(context.Context, string, string, string) (ExternalTarget, error)
	ProviderPortal(context.Context, string, string, string) (ExternalTarget, error)
}

type HTTPHandler struct{ service API }

func NewHTTPHandler(service API) http.Handler {
	handler := &HTTPHandler{service: service}
	router := chi.NewRouter()
	router.Get("/servers/{serverID}/console-options", handler.options)
	router.Post("/servers/{serverID}/console-sessions", handler.createSession)
	router.Post("/servers/{serverID}/console-window", handler.openWindow)
	router.Get("/servers/{serverID}/provider-portal", handler.providerPortal)
	return router
}

func (handler *HTTPHandler) options(response http.ResponseWriter, request *http.Request) {
	options, err := handler.service.Options(request.Context(), chi.URLParam(request, "serverID"))
	if err != nil {
		writeConsoleError(response, err)
		return
	}
	writeConsoleJSON(response, http.StatusOK, map[string]any{"options": options})
}

func (handler *HTTPHandler) createSession(response http.ResponseWriter, request *http.Request) {
	session, err := handler.service.CreateEmbedded(request.Context(), chi.URLParam(request, "serverID"), requestID(request), consoleSourceIP(request.RemoteAddr))
	if err != nil {
		writeConsoleError(response, err)
		return
	}
	writeConsoleJSON(response, http.StatusCreated, map[string]any{"session": session})
}

func (handler *HTTPHandler) openWindow(response http.ResponseWriter, request *http.Request) {
	target, err := handler.service.OpenWindow(request.Context(), chi.URLParam(request, "serverID"), requestID(request), consoleSourceIP(request.RemoteAddr))
	if err != nil {
		writeConsoleError(response, err)
		return
	}
	writeConsoleJSON(response, http.StatusOK, map[string]any{"target": target})
}

func (handler *HTTPHandler) providerPortal(response http.ResponseWriter, request *http.Request) {
	target, err := handler.service.ProviderPortal(request.Context(), chi.URLParam(request, "serverID"), requestID(request), consoleSourceIP(request.RemoteAddr))
	if err != nil {
		writeConsoleError(response, err)
		return
	}
	writeConsoleJSON(response, http.StatusOK, map[string]any{"target": target})
}

func requestID(request *http.Request) string {
	value := strings.TrimSpace(request.Header.Get("X-Request-ID"))
	if value == "" || len(value) > 255 {
		return uuid.NewString()
	}
	return value
}

func consoleSourceIP(remoteAddress string) string {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err == nil {
		return host
	}
	return remoteAddress
}

func writeConsoleError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, inventory.ErrNotFound):
		writeConsoleErrorResponse(response, http.StatusNotFound, "server_not_found", "Server was not found")
	case errors.Is(err, ErrCapabilityUnavailable):
		writeConsoleErrorResponse(response, http.StatusConflict, "capability_unavailable", "Console option is unavailable")
	case errors.Is(err, ErrTargetRejected):
		writeConsoleErrorResponse(response, http.StatusBadGateway, "console_target_rejected", "Console target was rejected by security policy")
	case errors.Is(err, ErrTargetUnavailable):
		writeConsoleErrorResponse(response, http.StatusBadGateway, "console_target_unavailable", "Console target is unavailable")
	default:
		writeConsoleErrorResponse(response, http.StatusInternalServerError, "internal_error", "Unable to open console")
	}
}

func writeConsoleJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func writeConsoleErrorResponse(response http.ResponseWriter, status int, code, message string) {
	writeConsoleJSON(response, status, map[string]any{"error": map[string]string{"code": code, "message": message, "request_id": uuid.NewString()}})
}
