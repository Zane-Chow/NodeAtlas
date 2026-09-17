package operations

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"controlpanel/internal/config"
	"controlpanel/internal/connections"
	"controlpanel/internal/database"
	"controlpanel/internal/inventory"
	"github.com/stretchr/testify/require"
)

func TestSQLiteRepositoryContract(t *testing.T) {
	db, dialect, err := database.Open(context.Background(), config.DatabaseConfig{
		URL: "sqlite://" + filepath.Join(t.TempDir(), "operations.db"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, database.Migrate(context.Background(), db, dialect))
	now := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	require.NoError(t, connections.NewSQLRepository(db, dialect).Create(context.Background(), connections.Connection{
		ID: "connection-a", Name: "Lab", ProviderType: "mock", Settings: json.RawMessage(`{}`),
		Enabled: true, HealthStatus: connections.HealthUnknown, CreatedAt: now, UpdatedAt: now,
	}, connections.CredentialRecord{Ciphertext: []byte("cipher"), Nonce: make([]byte, 12), KeyVersion: 1}))
	require.NoError(t, inventory.NewSQLRepository(db, dialect).ApplyCompleteSync(context.Background(), inventory.SyncSnapshot{
		ConnectionID: "connection-a", CompletedAt: now, Servers: []inventory.Server{{
			ID: "server-a", ConnectionID: "connection-a", ExternalID: "remote-a", Scope: "zone-a", Name: "api-01",
			State: inventory.StateStopped, RemoteState: "STOPPED", Spec: json.RawMessage(`{}`), Addresses: json.RawMessage(`[]`),
			Capabilities: json.RawMessage(`{"can_start":{"available":true}}`), LastSeenAt: now, LastStateCheckedAt: now,
			CreatedAt: now, UpdatedAt: now,
		}},
	}))
	runRepositoryContract(t, NewSQLRepository(db, dialect), now)
}

func runRepositoryContract(t *testing.T, repository Repository, now time.Time) {
	t.Helper()
	ctx := context.Background()
	firstInput := Operation{
		ID: "operation-a", ServerID: "server-a", ConnectionID: "connection-a", Action: ActionStart,
		Status: StatusQueued, IdempotencyKey: "idem-a", QueuedAt: now, UpdatedAt: now,
	}
	first, created, err := repository.CreateQueued(ctx, firstInput)
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, firstInput.ID, first.ID)

	replayed, created, err := repository.CreateQueued(ctx, Operation{
		ID: "operation-replay", ServerID: "server-a", ConnectionID: "connection-a", Action: ActionStart,
		Status: StatusQueued, IdempotencyKey: "idem-a", QueuedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second),
	})
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, "operation-a", replayed.ID)
	byKey, err := repository.FindByIdempotencyKey(ctx, "idem-a")
	require.NoError(t, err)
	require.Equal(t, "operation-a", byKey.ID)

	_, _, err = repository.CreateQueued(ctx, Operation{
		ID: "operation-b", ServerID: "server-a", ConnectionID: "connection-a", Action: ActionReboot,
		Status: StatusQueued, IdempotencyKey: "idem-b", QueuedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second),
	})
	require.ErrorIs(t, err, ErrActiveOperation)

	started := now.Add(2 * time.Second)
	running, err := repository.Transition(ctx, first.ID, StatusRunning, Transition{At: started})
	require.NoError(t, err)
	require.Equal(t, StatusRunning, running.Status)
	require.NotNil(t, running.StartedAt)
	require.Equal(t, started, *running.StartedAt)

	verifying, err := repository.Transition(ctx, first.ID, StatusVerifying, Transition{At: now.Add(3 * time.Second), ProviderRequestID: "provider-request-a"})
	require.NoError(t, err)
	require.Equal(t, "provider-request-a", verifying.ProviderRequestID)

	finished := now.Add(4 * time.Second)
	succeeded, err := repository.Transition(ctx, first.ID, StatusSucceeded, Transition{At: finished})
	require.NoError(t, err)
	require.NotNil(t, succeeded.FinishedAt)
	require.Equal(t, finished, *succeeded.FinishedAt)

	_, created, err = repository.CreateQueued(ctx, Operation{
		ID: "operation-b", ServerID: "server-a", ConnectionID: "connection-a", Action: ActionReboot,
		Status: StatusQueued, IdempotencyKey: "idem-b", QueuedAt: finished.Add(time.Second), UpdatedAt: finished.Add(time.Second),
	})
	require.NoError(t, err)
	require.True(t, created)

	_, err = repository.Transition(ctx, first.ID, StatusRunning, Transition{At: finished.Add(time.Second)})
	require.ErrorIs(t, err, ErrInvalidTransition)

	listed, err := repository.List(ctx, Filter{ServerID: "server-a"})
	require.NoError(t, err)
	require.Len(t, listed, 2)
	require.Equal(t, "operation-b", listed[0].ID)
	found, err := repository.FindByID(ctx, first.ID)
	require.NoError(t, err)
	require.Equal(t, StatusSucceeded, found.Status)
}
