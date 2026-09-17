package console

import (
	"context"
	"crypto/sha256"
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

func TestSQLiteRepositoryConsumesTicketOnceAndRecordsLifecycle(t *testing.T) {
	repository, now := openSQLiteConsoleRepository(t)
	ticketHash := sha256.Sum256([]byte("one-use-ticket"))
	require.NoError(t, repository.Create(context.Background(), Session{
		ID: "session-a", ServerID: "server-a", Mode: ModeEmbedded, TicketHash: ticketHash[:],
		ExpiresAt: now.Add(time.Minute), Result: ResultPending, CreatedAt: now,
	}))

	opened, err := repository.ConsumeTicket(context.Background(), ticketHash[:], now.Add(time.Second))
	require.NoError(t, err)
	require.Equal(t, "session-a", opened.ID)
	require.NotNil(t, opened.OpenedAt)
	_, err = repository.ConsumeTicket(context.Background(), ticketHash[:], now.Add(2*time.Second))
	require.ErrorIs(t, err, ErrTicketUnavailable)

	require.NoError(t, repository.Close(context.Background(), "session-a", ResultCompleted, now.Add(10*time.Second)))
	found, err := repository.FindByID(context.Background(), "session-a")
	require.NoError(t, err)
	require.Equal(t, ResultCompleted, found.Result)
	require.NotNil(t, found.ClosedAt)
}

func TestSQLiteRepositoryRejectsExpiredTicket(t *testing.T) {
	repository, now := openSQLiteConsoleRepository(t)
	ticketHash := sha256.Sum256([]byte("expired-ticket"))
	require.NoError(t, repository.Create(context.Background(), Session{
		ID: "session-expired", ServerID: "server-a", Mode: ModeEmbedded, TicketHash: ticketHash[:],
		ExpiresAt: now.Add(time.Minute), Result: ResultPending, CreatedAt: now,
	}))

	_, err := repository.ConsumeTicket(context.Background(), ticketHash[:], now.Add(2*time.Minute))
	require.ErrorIs(t, err, ErrTicketUnavailable)
}

func openSQLiteConsoleRepository(t *testing.T) (*SQLRepository, time.Time) {
	t.Helper()
	db, dialect, err := database.Open(context.Background(), config.DatabaseConfig{URL: "sqlite://" + filepath.Join(t.TempDir(), "console.db")})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, database.Migrate(context.Background(), db, dialect))
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	require.NoError(t, connections.NewSQLRepository(db, dialect).Create(context.Background(), connections.Connection{
		ID: "connection-a", Name: "Lab", ProviderType: "mock", Settings: json.RawMessage(`{}`), Enabled: true,
		HealthStatus: connections.HealthUnknown, CreatedAt: now, UpdatedAt: now,
	}, connections.CredentialRecord{Ciphertext: []byte("cipher"), Nonce: make([]byte, 12), KeyVersion: 1}))
	require.NoError(t, inventory.NewSQLRepository(db, dialect).ApplyCompleteSync(context.Background(), inventory.SyncSnapshot{
		ConnectionID: "connection-a", CompletedAt: now, Servers: []inventory.Server{{
			ID: "server-a", ConnectionID: "connection-a", ExternalID: "remote-a", Scope: "zone-a", Name: "api-01",
			State: inventory.StateRunning, RemoteState: "RUNNING", Spec: json.RawMessage(`{}`), Addresses: json.RawMessage(`[]`),
			Capabilities: json.RawMessage(`{}`), LastSeenAt: now, LastStateCheckedAt: now, CreatedAt: now, UpdatedAt: now,
		}},
	}))
	return NewSQLRepository(db, dialect), now
}
