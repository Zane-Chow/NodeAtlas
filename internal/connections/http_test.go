package connections

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	provideraws "controlpanel/internal/providers/aws"
	providergcp "controlpanel/internal/providers/gcp"
	providersolusvm2 "controlpanel/internal/providers/solusvm2"
	providervirtfusion "controlpanel/internal/providers/virtfusion"
	providervirtualizor "controlpanel/internal/providers/virtualizor"
	"github.com/stretchr/testify/require"
)

func TestHTTPCreateListAndGetConnectionWithoutCredentials(t *testing.T) {
	service, _, _, _ := newServiceFixture(t)
	handler := NewHTTPHandler(service)
	create := jsonRequest(t, http.MethodPost, "/connections", map[string]any{
		"name": "Lab A", "provider_type": "mock", "enabled": false,
		"settings": map[string]any{"server_count": 2}, "credentials": map[string]any{"token": "never-return-me"},
	})
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, create)
	require.Equal(t, http.StatusCreated, created.Code)
	require.NotContains(t, created.Body.String(), "never-return-me")
	require.NotContains(t, created.Body.String(), "credentials")

	listed := httptest.NewRecorder()
	handler.ServeHTTP(listed, httptest.NewRequest(http.MethodGet, "/connections", nil))
	require.Equal(t, http.StatusOK, listed.Code)
	require.Contains(t, listed.Body.String(), "Lab A")
	require.NotContains(t, listed.Body.String(), "credentials")

	detail := httptest.NewRecorder()
	handler.ServeHTTP(detail, httptest.NewRequest(http.MethodGet, "/connections/connection-id", nil))
	require.Equal(t, http.StatusOK, detail.Code)
}

func TestHTTPConnectionActionsAndProviderTypes(t *testing.T) {
	service, _, _, queue := newServiceFixture(t)
	require.NoError(t, service.registry.Register("aws", provideraws.NewFactory()))
	require.NoError(t, service.registry.Register("gcp", providergcp.NewFactory()))
	require.NoError(t, service.registry.Register("solusvm2", providersolusvm2.NewFactory(providersolusvm2.FactoryOptions{})))
	require.NoError(t, service.registry.Register("virtualizor", providervirtualizor.NewFactory(providervirtualizor.FactoryOptions{})))
	require.NoError(t, service.registry.Register("virtfusion", providervirtfusion.NewFactory(providervirtfusion.FactoryOptions{})))
	_, err := service.Create(t.Context(), CreateInput{
		Name: "Lab", ProviderType: "mock", Enabled: true,
		Settings: json.RawMessage(`{"server_count":1}`), Credentials: json.RawMessage(`{"token":"valid"}`),
	})
	require.NoError(t, err)
	queue.connectionIDs = nil
	handler := NewHTTPHandler(service)

	providerTypes := httptest.NewRecorder()
	handler.ServeHTTP(providerTypes, httptest.NewRequest(http.MethodGet, "/provider-types", nil))
	require.Equal(t, http.StatusOK, providerTypes.Code)
	require.JSONEq(t, `{
		"provider_types": [
			{"id":"aws","name":"AWS EC2"},
			{"id":"gcp","name":"Google Cloud Compute Engine"},
			{"id":"mock","name":"Mock Provider"},
			{"id":"solusvm2","name":"SolusVM 2"},
			{"id":"virtualizor","name":"Virtualizor"},
			{"id":"virtfusion","name":"VirtFusion"}
		]
	}`, providerTypes.Body.String())

	tested := httptest.NewRecorder()
	handler.ServeHTTP(tested, httptest.NewRequest(http.MethodPost, "/connections/connection-id/test", bytes.NewReader([]byte(`{}`))))
	require.Equal(t, http.StatusOK, tested.Code)
	require.Contains(t, tested.Body.String(), `"healthy":true`)

	synced := httptest.NewRecorder()
	handler.ServeHTTP(synced, httptest.NewRequest(http.MethodPost, "/connections/connection-id/sync", bytes.NewReader([]byte(`{}`))))
	require.Equal(t, http.StatusAccepted, synced.Code)
	require.Equal(t, []string{"connection-id"}, queue.connectionIDs)

	deleted := httptest.NewRecorder()
	handler.ServeHTTP(deleted, httptest.NewRequest(http.MethodDelete, "/connections/connection-id", nil))
	require.Equal(t, http.StatusNoContent, deleted.Code)
}

func TestHTTPRejectsUnknownJSONFields(t *testing.T) {
	service, _, _, _ := newServiceFixture(t)
	handler := NewHTTPHandler(service)
	request := httptest.NewRequest(http.MethodPost, "/connections", bytes.NewReader([]byte(`{
		"name":"Lab","provider_type":"mock","enabled":true,"settings":{},"credentials":{"token":"valid"},"extra":true
	}`)))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	require.Equal(t, http.StatusBadRequest, response.Code)
	require.Contains(t, response.Body.String(), `"code":"invalid_request"`)
}

func jsonRequest(t *testing.T, method, path string, value any) *http.Request {
	t.Helper()
	body, err := json.Marshal(value)
	require.NoError(t, err)
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	return request
}
