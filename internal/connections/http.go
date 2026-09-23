package connections

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type HTTPHandler struct{ service *Service }

func NewHTTPHandler(service *Service) http.Handler {
	handler := &HTTPHandler{service: service}
	router := chi.NewRouter()
	router.Get("/provider-types", handler.providerTypes)
	router.Get("/connections", handler.list)
	router.Post("/connections", handler.create)
	router.Route("/connections/{connectionID}", func(connection chi.Router) {
		connection.Get("/", handler.get)
		connection.Put("/", handler.update)
		connection.Delete("/", handler.delete)
		connection.Post("/test", handler.test)
		connection.Post("/sync", handler.sync)
	})
	return router
}

func (handler *HTTPHandler) providerTypes(response http.ResponseWriter, _ *http.Request) {
	providerTypes := handler.service.ProviderTypes()
	order := map[string]int{"aws": 0, "gcp": 1, "mock": 2, "solusvm2": 3, "virtualizor": 4, "virtfusion": 5}
	sort.SliceStable(providerTypes, func(i, j int) bool {
		left, leftKnown := order[providerTypes[i]]
		right, rightKnown := order[providerTypes[j]]
		if leftKnown != rightKnown {
			return leftKnown
		}
		if leftKnown {
			return left < right
		}
		return providerTypes[i] < providerTypes[j]
	})
	types := make([]map[string]string, 0, len(providerTypes))
	for _, providerType := range providerTypes {
		name := strings.ToUpper(providerType[:1]) + providerType[1:] + " Provider"
		if providerType == "mock" {
			name = "Mock Provider"
		} else if providerType == "aws" {
			name = "AWS EC2"
		} else if providerType == "gcp" {
			name = "Google Cloud Compute Engine"
		} else if providerType == "solusvm2" {
			name = "SolusVM 2"
		} else if providerType == "virtualizor" {
			name = "Virtualizor"
		} else if providerType == "virtfusion" {
			name = "VirtFusion"
		}
		types = append(types, map[string]string{"id": providerType, "name": name})
	}
	writeJSON(response, http.StatusOK, map[string]any{"provider_types": types})
}

func (handler *HTTPHandler) list(response http.ResponseWriter, request *http.Request) {
	connections, err := handler.service.List(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "internal_error", "Unable to list provider connections")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"connections": connections})
}

func (handler *HTTPHandler) get(response http.ResponseWriter, request *http.Request) {
	connection, err := handler.service.Find(request.Context(), chi.URLParam(request, "connectionID"))
	if err != nil {
		writeServiceError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"connection": connection})
}

type connectionInput struct {
	Name         string          `json:"name"`
	ProviderType string          `json:"provider_type"`
	Endpoint     string          `json:"endpoint"`
	Settings     json.RawMessage `json:"settings"`
	Credentials  json.RawMessage `json:"credentials"`
	Enabled      bool            `json:"enabled"`
}

func (handler *HTTPHandler) create(response http.ResponseWriter, request *http.Request) {
	var input connectionInput
	if err := decodeJSON(response, request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", "Request body is invalid")
		return
	}
	connection, err := handler.service.Create(request.Context(), CreateInput{
		Name: input.Name, ProviderType: input.ProviderType, Endpoint: input.Endpoint,
		Settings: input.Settings, Credentials: input.Credentials, Enabled: input.Enabled,
	})
	if err != nil {
		writeServiceError(response, err)
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{"connection": connection})
}

func (handler *HTTPHandler) update(response http.ResponseWriter, request *http.Request) {
	var input connectionInput
	if err := decodeJSON(response, request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", "Request body is invalid")
		return
	}
	connection, err := handler.service.Update(request.Context(), chi.URLParam(request, "connectionID"), UpdateInput{
		Name: input.Name, Endpoint: input.Endpoint, Settings: input.Settings, Credentials: input.Credentials, Enabled: input.Enabled,
	})
	if err != nil {
		writeServiceError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"connection": connection})
}

func (handler *HTTPHandler) delete(response http.ResponseWriter, request *http.Request) {
	if err := handler.service.Delete(request.Context(), chi.URLParam(request, "connectionID")); err != nil {
		writeServiceError(response, err)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (handler *HTTPHandler) test(response http.ResponseWriter, request *http.Request) {
	result, err := handler.service.Test(request.Context(), chi.URLParam(request, "connectionID"))
	if err != nil {
		writeServiceError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *HTTPHandler) sync(response http.ResponseWriter, request *http.Request) {
	if err := handler.service.RequestSync(request.Context(), chi.URLParam(request, "connectionID")); err != nil {
		writeServiceError(response, err)
		return
	}
	writeJSON(response, http.StatusAccepted, map[string]string{"status": "queued"})
}

func decodeJSON(response http.ResponseWriter, request *http.Request, target any) error {
	request.Body = http.MaxBytesReader(response, request.Body, 64*1024)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON value")
	}
	return nil
}

func writeServiceError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(response, http.StatusNotFound, "connection_not_found", "Provider connection was not found")
	case errors.Is(err, ErrInvalidConnection):
		writeError(response, http.StatusBadRequest, "invalid_connection", "Provider connection settings are invalid")
	case errors.Is(err, ErrConnectionDisabled):
		writeError(response, http.StatusConflict, "connection_disabled", "Provider connection is disabled")
	default:
		writeError(response, http.StatusInternalServerError, "internal_error", "Unable to process provider connection")
	}
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}
func writeError(response http.ResponseWriter, status int, code, message string) {
	writeJSON(response, status, map[string]any{"error": map[string]string{"code": code, "message": message, "request_id": uuid.NewString()}})
}
