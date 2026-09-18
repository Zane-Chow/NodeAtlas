package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type API interface {
	List(context.Context) ([]Metadata, error)
	Create(context.Context, string, Kind, string, string) (Metadata, error)
	Download(context.Context, string) ([]byte, string, error)
	Validate(context.Context, string, string) (Validation, error)
	Restore(context.Context, string, string, string, string) error
}

type HTTPHandler struct{ service API }

func NewHTTPHandler(service API) http.Handler {
	handler := &HTTPHandler{service: service}
	router := chi.NewRouter()
	router.Get("/backups", handler.list)
	router.Post("/backups", handler.create)
	router.Post("/backups/validate", handler.validate)
	router.Get("/backups/{backupID}/download", handler.download)
	router.Post("/backups/{backupID}/restore", handler.restore)
	return router
}

type passphraseRequest struct {
	Passphrase string `json:"passphrase"`
	BackupID   string `json:"backup_id,omitempty"`
}

func (handler *HTTPHandler) list(response http.ResponseWriter, request *http.Request) {
	items, err := handler.service.List(request.Context())
	if err != nil {
		writeBackupError(response, err)
		return
	}
	writeBackupJSON(response, http.StatusOK, map[string]any{"backups": items, "total": len(items)})
}

func (handler *HTTPHandler) create(response http.ResponseWriter, request *http.Request) {
	input, ok := decodePassphrase(response, request)
	if !ok {
		return
	}
	metadata, err := handler.service.Create(request.Context(), input.Passphrase, KindManual, backupRequestID(request), backupSourceIP(request.RemoteAddr))
	if err != nil {
		writeBackupError(response, err)
		return
	}
	writeBackupJSON(response, http.StatusCreated, map[string]any{"backup": metadata})
}

func (handler *HTTPHandler) download(response http.ResponseWriter, request *http.Request) {
	data, filename, err := handler.service.Download(request.Context(), chi.URLParam(request, "backupID"))
	if err != nil {
		writeBackupError(response, err)
		return
	}
	response.Header().Set("Content-Type", "application/octet-stream")
	response.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(data)
}

func (handler *HTTPHandler) validate(response http.ResponseWriter, request *http.Request) {
	input, ok := decodePassphrase(response, request)
	if !ok {
		return
	}
	validation, err := handler.service.Validate(request.Context(), strings.TrimSpace(input.BackupID), input.Passphrase)
	if err != nil {
		writeBackupError(response, err)
		return
	}
	writeBackupJSON(response, http.StatusOK, map[string]any{"validation": validation})
}

func (handler *HTTPHandler) restore(response http.ResponseWriter, request *http.Request) {
	input, ok := decodePassphrase(response, request)
	if !ok {
		return
	}
	err := handler.service.Restore(request.Context(), chi.URLParam(request, "backupID"), input.Passphrase, backupRequestID(request), backupSourceIP(request.RemoteAddr))
	if err != nil {
		writeBackupError(response, err)
		return
	}
	writeBackupJSON(response, http.StatusOK, map[string]string{"status": "restored"})
}

func decodePassphrase(response http.ResponseWriter, request *http.Request) (passphraseRequest, bool) {
	request.Body = http.MaxBytesReader(response, request.Body, 4096)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input passphraseRequest
	if err := decoder.Decode(&input); err != nil || len(input.Passphrase) < 12 || len(input.Passphrase) > 1024 {
		writeBackupErrorResponse(response, http.StatusBadRequest, "invalid_request", "A backup passphrase of at least 12 characters is required")
		return passphraseRequest{}, false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeBackupErrorResponse(response, http.StatusBadRequest, "invalid_request", "Request body is invalid")
		return passphraseRequest{}, false
	}
	return input, true
}

func writeBackupError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeBackupErrorResponse(response, http.StatusNotFound, "backup_not_found", "Backup was not found")
	case errors.Is(err, ErrActiveWork):
		writeBackupErrorResponse(response, http.StatusConflict, "active_work", "Wait for active operations and jobs before restoring")
	case errors.Is(err, ErrInvalidBackup), errors.Is(err, ErrInvalidManifest), errors.Is(err, ErrChecksumMismatch):
		writeBackupErrorResponse(response, http.StatusUnprocessableEntity, "backup_invalid", "Backup validation failed")
	default:
		writeBackupErrorResponse(response, http.StatusInternalServerError, "internal_error", "Unable to process backup")
	}
}

func backupRequestID(request *http.Request) string {
	value := strings.TrimSpace(request.Header.Get("X-Request-ID"))
	if value == "" || len(value) > 255 {
		return uuid.NewString()
	}
	return value
}
func backupSourceIP(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err == nil {
		return host
	}
	return address
}
func writeBackupJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}
func writeBackupErrorResponse(response http.ResponseWriter, status int, code, message string) {
	writeBackupJSON(response, status, map[string]any{"error": map[string]string{"code": code, "message": message, "request_id": uuid.NewString()}})
}
