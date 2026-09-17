package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
)

type readinessFunc func(context.Context) error

func (fn readinessFunc) PingContext(ctx context.Context) error { return fn(ctx) }

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

func TestHealthReadyReflectsDatabaseStatus(t *testing.T) {
	tests := []struct {
		name       string
		pingError  error
		statusCode int
	}{
		{name: "ready", statusCode: http.StatusOK},
		{name: "database unavailable", pingError: errors.New("offline"), statusCode: http.StatusServiceUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := NewRouter(Dependencies{
				Assets: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("app")}},
				Readiness: readinessFunc(func(context.Context) error {
					return test.pingError
				}),
			})
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
			require.Equal(t, test.statusCode, response.Code)
		})
	}
}
