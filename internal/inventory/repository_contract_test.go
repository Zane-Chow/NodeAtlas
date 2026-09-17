package inventory

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"controlpanel/internal/config"
	"controlpanel/internal/connections"
	"controlpanel/internal/database"
	"controlpanel/internal/providers"
	"github.com/stretchr/testify/require"
)

func TestSQLiteRepositoryContract(t *testing.T) {
	db, dialect, err := database.Open(context.Background(), config.DatabaseConfig{
		URL: "sqlite://" + filepath.Join(t.TempDir(), "inventory.db"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, database.Migrate(context.Background(), db, dialect))
	now := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	connectionRepository := connections.NewSQLRepository(db, dialect)
	require.NoError(t, connectionRepository.Create(context.Background(), connections.Connection{
		ID: "connection-a", Name: "Lab", ProviderType: "mock", Settings: json.RawMessage(`{}`),
		Enabled: true, HealthStatus: connections.HealthUnknown, CreatedAt: now, UpdatedAt: now,
	}, connections.CredentialRecord{Ciphertext: []byte("cipher"), Nonce: make([]byte, 12), KeyVersion: 1}))
	runRepositoryContract(t, NewSQLRepository(db, dialect), now)
}

func runRepositoryContract(t *testing.T, repository Repository, now time.Time) {
	ctx := context.Background()
	first := serverFixture("server-a", "remote-a", "api-01", StateRunning, now)
	second := serverFixture("server-b", "remote-b", "db-01", StateStopped, now)
	require.NoError(t, repository.ApplyCompleteSync(ctx, SyncSnapshot{
		ConnectionID: "connection-a", CompletedAt: now, Servers: []Server{first, second},
	}))

	listed, err := repository.List(ctx, Filter{})
	require.NoError(t, err)
	require.Equal(t, []string{"api-01", "db-01"}, []string{listed[0].Name, listed[1].Name})
	require.NoError(t, repository.UpdateRemote(ctx, "server-a", providers.RemoteServer{
		ExternalID: "remote-a", Scope: "zone-a", Name: "api-01", State: providers.StateStopped, RemoteState: "STOPPED",
		Spec: json.RawMessage(`{"cpu":4}`), Addresses: json.RawMessage(`[{"address":"10.0.0.2"}]`),
		Capabilities: providers.Capabilities{CanStart: providers.Capability{Available: true}},
	}, now.Add(30*time.Second)))
	updated, err := repository.FindByID(ctx, "server-a")
	require.NoError(t, err)
	require.Equal(t, StateStopped, updated.State)
	require.Equal(t, "STOPPED", updated.RemoteState)
	require.JSONEq(t, `{"cpu":4}`, string(updated.Spec))

	refreshed := first
	refreshed.Name = "api-renamed"
	refreshed.State = StateStopped
	refreshed.UpdatedAt = now.Add(time.Minute)
	require.NoError(t, repository.ApplyCompleteSync(ctx, SyncSnapshot{
		ConnectionID: "connection-a", CompletedAt: now.Add(time.Minute), Servers: []Server{refreshed},
	}))

	listed, err = repository.List(ctx, Filter{})
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, "server-a", listed[0].ID)
	require.Equal(t, "api-renamed", listed[0].Name)
	require.Equal(t, StateStopped, listed[0].State)

	badFirst := serverFixture("server-c", "remote-c", "cache-01", StateRunning, now.Add(2*time.Minute))
	badSecond := serverFixture("server-a", "remote-d", "collision", StateRunning, now.Add(2*time.Minute))
	err = repository.ApplyCompleteSync(ctx, SyncSnapshot{
		ConnectionID: "connection-a", CompletedAt: now.Add(2 * time.Minute), Servers: []Server{badFirst, badSecond},
	})
	require.Error(t, err)
	listed, err = repository.List(ctx, Filter{})
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, "api-renamed", listed[0].Name)

	filtered, err := repository.List(ctx, Filter{State: StateStopped, Query: "API"})
	require.NoError(t, err)
	require.Len(t, filtered, 1)
	_, err = repository.FindByID(ctx, "server-b")
	require.ErrorIs(t, err, ErrNotFound)
}

func serverFixture(id, externalID, name string, state State, at time.Time) Server {
	return Server{
		ID: id, ConnectionID: "connection-a", ExternalID: externalID, Scope: "zone-a", Name: name,
		State: state, RemoteState: string(state), Spec: json.RawMessage(`{"cpu":2}`),
		Addresses: json.RawMessage(`[{"address":"10.0.0.1"}]`), Capabilities: json.RawMessage(`{"can_start":{"available":true}}`),
		LastSeenAt: at, LastStateCheckedAt: at, CreatedAt: at, UpdatedAt: at,
	}
}
