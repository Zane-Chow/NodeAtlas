package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
)

func TestHealthLiveReturnsJSON(t *testing.T) {
	router := NewRouter(Dependencies{Assets: fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("app-shell")},
	}})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health/live", nil))

	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "application/json", response.Header().Get("Content-Type"))
	require.JSONEq(t, `{"status":"ok"}`, response.Body.String())
}

func TestUnknownFrontendRouteServesIndex(t *testing.T) {
	router := NewRouter(Dependencies{Assets: fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("app-shell")},
	}})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/servers/demo", nil))

	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "app-shell", response.Body.String())
}

func TestAPIRouteDoesNotFallBackToFrontend(t *testing.T) {
	router := NewRouter(Dependencies{Assets: fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("app-shell")},
	}})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/missing", nil))

	require.Equal(t, http.StatusNotFound, response.Code)
	require.NotContains(t, response.Body.String(), "app-shell")
}
