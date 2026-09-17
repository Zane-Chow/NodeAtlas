package inventory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"controlpanel/internal/config"
	"controlpanel/internal/connections"
	"controlpanel/internal/database"
	"github.com/stretchr/testify/require"
)

type recordingSyncRequester struct{ connectionIDs []string }

func (requester *recordingSyncRequester) RequestSync(_ context.Context, connectionID string) error {
	requester.connectionIDs = append(requester.connectionIDs, connectionID)
	return nil
}

func TestHTTPListsFiltersAndFindsLocalServers(t *testing.T) {
	repository := newHTTPInventoryRepository(t)
	requester := &recordingSyncRequester{}
	handler := NewHTTPHandler(repository, requester)

	listed := httptest.NewRecorder()
	handler.ServeHTTP(listed, httptest.NewRequest(http.MethodGet, "/servers?state=running&q=API&connection_id=connection-a&provider_type=mock", nil))
	require.Equal(t, http.StatusOK, listed.Code)
	var payload struct {
		Servers []Server `json:"servers"`
		Total   int      `json:"total"`
	}
	require.NoError(t, json.Unmarshal(listed.Body.Bytes(), &payload))
	require.Equal(t, 1, payload.Total)
	require.Equal(t, "api-01", payload.Servers[0].Name)

	detail := httptest.NewRecorder()
	handler.ServeHTTP(detail, httptest.NewRequest(http.MethodGet, "/servers/server-a", nil))
	require.Equal(t, http.StatusOK, detail.Code)
	require.Contains(t, detail.Body.String(), `"name":"api-01"`)

	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/servers/missing", nil))
	require.Equal(t, http.StatusNotFound, missing.Code)
}

func TestHTTPRefreshQueuesOwningConnection(t *testing.T) {
	repository := newHTTPInventoryRepository(t)
	requester := &recordingSyncRequester{}
	handler := NewHTTPHandler(repository, requester)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/servers/server-a/refresh", nil))

	require.Equal(t, http.StatusAccepted, response.Code)
	require.Equal(t, []string{"connection-a"}, requester.connectionIDs)
}

func TestHTTPRejectsUnknownStateFilter(t *testing.T) {
	repository := newHTTPInventoryRepository(t)
	handler := NewHTTPHandler(repository, &recordingSyncRequester{})
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/servers?state=not-real", nil))

	require.Equal(t, http.StatusBadRequest, response.Code)
	require.Contains(t, response.Body.String(), `"code":"invalid_filter"`)
}

func newHTTPInventoryRepository(t *testing.T) *SQLRepository {
	t.Helper()
	db, dialect, err := database.Open(context.Background(), config.DatabaseConfig{URL: "sqlite://" + filepath.Join(t.TempDir(), "http.db")})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, database.Migrate(context.Background(), db, dialect))
	now := time.Date(2026, 9, 18, 13, 0, 0, 0, time.UTC)
	require.NoError(t, connections.NewSQLRepository(db, dialect).Create(context.Background(), connections.Connection{
		ID: "connection-a", Name: "Lab", ProviderType: "mock", Settings: json.RawMessage(`{}`), Enabled: true,
		HealthStatus: connections.HealthUnknown, CreatedAt: now, UpdatedAt: now,
	}, connections.CredentialRecord{Ciphertext: []byte("cipher"), Nonce: make([]byte, 12), KeyVersion: 1}))
	repository := NewSQLRepository(db, dialect)
	require.NoError(t, repository.ApplyCompleteSync(context.Background(), SyncSnapshot{
		ConnectionID: "connection-a", CompletedAt: now,
		Servers: []Server{
			serverFixture("server-a", "remote-a", "api-01", StateRunning, now),
			serverFixture("server-b", "remote-b", "db-01", StateStopped, now),
		},
	}))
	return repository
}
