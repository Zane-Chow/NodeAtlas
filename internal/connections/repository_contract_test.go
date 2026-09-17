package connections

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"controlpanel/internal/config"
	"controlpanel/internal/database"
	"github.com/stretchr/testify/require"
)

func TestSQLiteRepositoryContract(t *testing.T) {
	db, dialect, err := database.Open(context.Background(), config.DatabaseConfig{
		URL: "sqlite://" + filepath.Join(t.TempDir(), "connections.db"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, database.Migrate(context.Background(), db, dialect))
	runRepositoryContract(t, NewSQLRepository(db, dialect))
}

func runRepositoryContract(t *testing.T, repository Repository) {
	ctx := context.Background()
	now := time.Date(2026, 9, 18, 8, 0, 0, 0, time.UTC)
	first := Connection{
		ID: "11111111-1111-4111-8111-111111111111", Name: "Lab A", ProviderType: "mock",
		Settings: json.RawMessage(`{"server_count":2}`), Enabled: true, HealthStatus: HealthUnknown,
		CreatedAt: now, UpdatedAt: now,
	}
	second := Connection{
		ID: "22222222-2222-4222-8222-222222222222", Name: "Lab B", ProviderType: "mock",
		Settings: json.RawMessage(`{"server_count":3}`), Enabled: true, HealthStatus: HealthUnknown,
		CreatedAt: now, UpdatedAt: now,
	}
	firstCredentials := CredentialRecord{Ciphertext: []byte("encrypted-a"), Nonce: []byte("123456789012"), KeyVersion: 1}
	secondCredentials := CredentialRecord{Ciphertext: []byte("encrypted-b"), Nonce: []byte("abcdefghijkl"), KeyVersion: 1}

	require.NoError(t, repository.Create(ctx, first, firstCredentials))
	require.NoError(t, repository.Create(ctx, second, secondCredentials))

	listed, err := repository.List(ctx)
	require.NoError(t, err)
	require.Len(t, listed, 2)
	serialized, err := json.Marshal(listed)
	require.NoError(t, err)
	require.NotContains(t, string(serialized), "encrypted-a")
	require.NotContains(t, string(serialized), "credentials")

	stored, credentials, err := repository.FindByID(ctx, first.ID)
	require.NoError(t, err)
	require.Equal(t, "Lab A", stored.Name)
	require.Equal(t, firstCredentials, credentials)

	stored.Name = "Renamed"
	stored.Enabled = false
	stored.Settings = json.RawMessage(`{"server_count":4}`)
	stored.UpdatedAt = now.Add(time.Minute)
	require.NoError(t, repository.Update(ctx, stored, nil))
	updated, preservedCredentials, err := repository.FindByID(ctx, first.ID)
	require.NoError(t, err)
	require.Equal(t, "Renamed", updated.Name)
	require.False(t, updated.Enabled)
	require.Equal(t, firstCredentials, preservedCredentials)

	testedAt := now.Add(2 * time.Minute)
	require.NoError(t, repository.UpdateHealth(ctx, first.ID, HealthHealthy, "", "", testedAt))
	updated, _, err = repository.FindByID(ctx, first.ID)
	require.NoError(t, err)
	require.Equal(t, HealthHealthy, updated.HealthStatus)
	require.WithinDuration(t, testedAt, *updated.LastTestedAt, time.Microsecond)

	require.NoError(t, repository.SoftDelete(ctx, first.ID, now.Add(3*time.Minute)))
	_, _, err = repository.FindByID(ctx, first.ID)
	require.ErrorIs(t, err, ErrNotFound)
	listed, err = repository.List(ctx)
	require.NoError(t, err)
	require.Len(t, listed, 1)
}
