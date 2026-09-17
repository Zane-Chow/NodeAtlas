package connections

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"controlpanel/internal/config"
	"controlpanel/internal/database"
	"controlpanel/internal/providers"
	providermock "controlpanel/internal/providers/mock"
	"controlpanel/internal/secrets"
	"github.com/stretchr/testify/require"
)

type recordingSyncQueue struct {
	connectionIDs []string
}

func (queue *recordingSyncQueue) EnqueueConnectionSync(_ context.Context, connectionID string) error {
	queue.connectionIDs = append(queue.connectionIDs, connectionID)
	return nil
}

func TestServiceCreateEncryptsCredentialsAndQueuesInitialSync(t *testing.T) {
	service, repository, cipher, queue := newServiceFixture(t)

	created, err := service.Create(context.Background(), CreateInput{
		Name: "Lab A", ProviderType: "mock", Enabled: true,
		Settings:    json.RawMessage(`{"server_count":4,"seed":9}`),
		Credentials: json.RawMessage(`{"token":"write-only-token"}`),
	})

	require.NoError(t, err)
	require.Equal(t, "connection-id", created.ID)
	require.Equal(t, []string{"connection-id"}, queue.connectionIDs)
	_, storedCredentials, err := repository.FindByID(context.Background(), created.ID)
	require.NoError(t, err)
	require.NotContains(t, string(storedCredentials.Ciphertext), "write-only-token")
	plaintext, err := cipher.Decrypt(created.ID, "mock", secrets.Envelope{
		Ciphertext: storedCredentials.Ciphertext, Nonce: storedCredentials.Nonce, KeyVersion: storedCredentials.KeyVersion,
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"token":"write-only-token"}`, string(plaintext))
	serialized, err := json.Marshal(created)
	require.NoError(t, err)
	require.NotContains(t, string(serialized), "token")
}

func TestServiceDisabledConnectionDoesNotQueueSync(t *testing.T) {
	service, _, _, queue := newServiceFixture(t)

	_, err := service.Create(context.Background(), CreateInput{
		Name: "Offline Lab", ProviderType: "mock", Enabled: false,
		Settings: json.RawMessage(`{"server_count":1}`), Credentials: json.RawMessage(`{"token":"valid"}`),
	})

	require.NoError(t, err)
	require.Empty(t, queue.connectionIDs)
}

func TestServiceRejectsInvalidProviderSettingsBeforePersistence(t *testing.T) {
	service, repository, _, _ := newServiceFixture(t)

	_, err := service.Create(context.Background(), CreateInput{
		Name: "Broken", ProviderType: "mock", Enabled: true,
		Settings: json.RawMessage(`{"unexpected":true}`), Credentials: json.RawMessage(`{"token":"valid"}`),
	})

	require.ErrorIs(t, err, ErrInvalidConnection)
	connections, listErr := repository.List(context.Background())
	require.NoError(t, listErr)
	require.Empty(t, connections)
}

func TestServiceTestConnectionUpdatesHealth(t *testing.T) {
	service, repository, _, _ := newServiceFixture(t)
	created, err := service.Create(context.Background(), CreateInput{
		Name: "Rate Limited", ProviderType: "mock", Enabled: false,
		Settings:    json.RawMessage(`{"server_count":1,"health_mode":"rate_limited"}`),
		Credentials: json.RawMessage(`{"token":"valid"}`),
	})
	require.NoError(t, err)

	result, err := service.Test(context.Background(), created.ID)

	require.NoError(t, err)
	require.False(t, result.Healthy)
	require.Equal(t, string(providers.ErrorRateLimited), result.Code)
	stored, _, err := repository.FindByID(context.Background(), created.ID)
	require.NoError(t, err)
	require.Equal(t, HealthDegraded, stored.HealthStatus)
	require.Equal(t, string(providers.ErrorRateLimited), stored.LastErrorCode)
}

func TestServiceUpdatePreservesCredentialsWhenOmitted(t *testing.T) {
	service, repository, cipher, queue := newServiceFixture(t)
	created, err := service.Create(context.Background(), CreateInput{
		Name: "Original", ProviderType: "mock", Enabled: false,
		Settings: json.RawMessage(`{"server_count":1}`), Credentials: json.RawMessage(`{"token":"keep-me"}`),
	})
	require.NoError(t, err)
	_, before, err := repository.FindByID(context.Background(), created.ID)
	require.NoError(t, err)

	updated, err := service.Update(context.Background(), created.ID, UpdateInput{
		Name: "Renamed", Endpoint: "", Settings: json.RawMessage(`{"server_count":2}`), Enabled: true,
	})

	require.NoError(t, err)
	require.Equal(t, "Renamed", updated.Name)
	require.Equal(t, []string{created.ID}, queue.connectionIDs)
	_, after, err := repository.FindByID(context.Background(), created.ID)
	require.NoError(t, err)
	require.Equal(t, before, after)
	plaintext, err := cipher.Decrypt(created.ID, "mock", secrets.Envelope{Ciphertext: after.Ciphertext, Nonce: after.Nonce, KeyVersion: after.KeyVersion})
	require.NoError(t, err)
	require.JSONEq(t, `{"token":"keep-me"}`, string(plaintext))
}

func TestServiceDeleteAndManualSync(t *testing.T) {
	service, repository, _, queue := newServiceFixture(t)
	created, err := service.Create(context.Background(), CreateInput{
		Name: "Lab", ProviderType: "mock", Enabled: false,
		Settings: json.RawMessage(`{"server_count":1}`), Credentials: json.RawMessage(`{"token":"valid"}`),
	})
	require.NoError(t, err)
	require.ErrorIs(t, service.RequestSync(context.Background(), created.ID), ErrConnectionDisabled)

	_, err = service.Update(context.Background(), created.ID, UpdateInput{
		Name: "Lab", Settings: json.RawMessage(`{"server_count":1}`), Enabled: true,
	})
	require.NoError(t, err)
	queue.connectionIDs = nil
	require.NoError(t, service.RequestSync(context.Background(), created.ID))
	require.Equal(t, []string{created.ID}, queue.connectionIDs)
	require.NoError(t, service.Delete(context.Background(), created.ID))
	_, _, err = repository.FindByID(context.Background(), created.ID)
	require.ErrorIs(t, err, ErrNotFound)
}

func newServiceFixture(t *testing.T) (*Service, *SQLRepository, *secrets.CredentialCipher, *recordingSyncQueue) {
	t.Helper()
	db, dialect, err := database.Open(context.Background(), config.DatabaseConfig{
		URL: "sqlite://" + filepath.Join(t.TempDir(), "service.db"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, database.Migrate(context.Background(), db, dialect))
	repository := NewSQLRepository(db, dialect)
	cipher, err := secrets.NewCredentialCipher(map[int][]byte{1: bytes.Repeat([]byte{5}, 32)}, 1)
	require.NoError(t, err)
	registry := providers.NewRegistry()
	require.NoError(t, registry.Register("mock", providermock.NewFactory()))
	queue := &recordingSyncQueue{}
	service := NewService(repository, cipher, registry, queue, ServiceOptions{
		Now:   func() time.Time { return time.Date(2026, 9, 18, 9, 0, 0, 0, time.UTC) },
		NewID: func() string { return "connection-id" },
	})
	return service, repository, cipher, queue
}
