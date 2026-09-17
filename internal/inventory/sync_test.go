package inventory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"controlpanel/internal/config"
	"controlpanel/internal/connections"
	"controlpanel/internal/database"
	"controlpanel/internal/providers"
	providermock "controlpanel/internal/providers/mock"
	"controlpanel/internal/secrets"
	"github.com/stretchr/testify/require"
)

func TestSyncerImportsMockProviderInventory(t *testing.T) {
	fixture := newSyncFixture(t, "mock", providermock.NewFactory(), json.RawMessage(`{"server_count":3,"seed":2}`))

	err := fixture.syncer.SyncConnection(context.Background(), "connection-a")

	require.NoError(t, err)
	servers, err := fixture.inventory.List(context.Background(), Filter{})
	require.NoError(t, err)
	require.Len(t, servers, 3)
	require.Equal(t, "connection-a-server-001", servers[0].ExternalID)
	stored, _, err := fixture.connections.FindByID(context.Background(), "connection-a")
	require.NoError(t, err)
	require.Equal(t, connections.HealthHealthy, stored.HealthStatus)
	require.NotNil(t, stored.LastSyncedAt)
}

func TestSyncerSecondPageFailurePreservesPriorInventory(t *testing.T) {
	factory := &scriptedFactory{provider: &scriptedProvider{}}
	fixture := newSyncFixture(t, "scripted", factory, json.RawMessage(`{}`))
	prior := serverFixture("prior-id", "prior-remote", "prior-server", StateRunning, fixture.now)
	require.NoError(t, fixture.inventory.ApplyCompleteSync(context.Background(), SyncSnapshot{
		ConnectionID: "connection-a", CompletedAt: fixture.now, Servers: []Server{prior},
	}))

	err := fixture.syncer.SyncConnection(context.Background(), "connection-a")

	require.ErrorContains(t, err, "second page failed")
	servers, listErr := fixture.inventory.List(context.Background(), Filter{})
	require.NoError(t, listErr)
	require.Len(t, servers, 1)
	require.Equal(t, "prior-server", servers[0].Name)
	stored, _, findErr := fixture.connections.FindByID(context.Background(), "connection-a")
	require.NoError(t, findErr)
	require.Equal(t, connections.HealthDegraded, stored.HealthStatus)
}

type syncFixture struct {
	syncer      *Syncer
	inventory   *SQLRepository
	connections *connections.SQLRepository
	now         time.Time
}

func newSyncFixture(t *testing.T, providerType string, factory providers.Factory, settings json.RawMessage) syncFixture {
	t.Helper()
	db, dialect, err := database.Open(context.Background(), config.DatabaseConfig{URL: "sqlite://" + filepath.Join(t.TempDir(), "sync.db")})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, database.Migrate(context.Background(), db, dialect))
	now := time.Date(2026, 9, 18, 11, 0, 0, 0, time.UTC)
	connectionRepository := connections.NewSQLRepository(db, dialect)
	cipher, err := secrets.NewCredentialCipher(map[int][]byte{1: bytes.Repeat([]byte{6}, 32)}, 1)
	require.NoError(t, err)
	plaintext := json.RawMessage(`{"token":"valid"}`)
	envelope, err := cipher.Encrypt("connection-a", providerType, plaintext)
	require.NoError(t, err)
	require.NoError(t, connectionRepository.Create(context.Background(), connections.Connection{
		ID: "connection-a", Name: "Lab", ProviderType: providerType, Settings: settings, Enabled: true,
		HealthStatus: connections.HealthUnknown, CreatedAt: now, UpdatedAt: now,
	}, connections.CredentialRecord{Ciphertext: envelope.Ciphertext, Nonce: envelope.Nonce, KeyVersion: envelope.KeyVersion}))
	registry := providers.NewRegistry()
	require.NoError(t, registry.Register(providerType, factory))
	inventoryRepository := NewSQLRepository(db, dialect)
	idSequence := 0
	syncer := NewSyncer(connectionRepository, inventoryRepository, cipher, registry, SyncerOptions{
		Now:   func() time.Time { return now.Add(time.Minute) },
		NewID: func() string { idSequence++; return fmt.Sprintf("new-server-%d", idSequence) },
	})
	return syncFixture{syncer: syncer, inventory: inventoryRepository, connections: connectionRepository, now: now}
}

type scriptedFactory struct{ provider providers.Provider }

func (factory *scriptedFactory) Create(providers.ConnectionConfig) (providers.Provider, error) {
	return factory.provider, nil
}

type scriptedProvider struct{ calls int }

func (*scriptedProvider) ValidateConnection(context.Context) (providers.ConnectionInfo, error) {
	return providers.ConnectionInfo{}, nil
}
func (provider *scriptedProvider) ListServers(context.Context, *providers.Cursor) (providers.ServerPage, error) {
	provider.calls++
	if provider.calls == 1 {
		return providers.ServerPage{Servers: []providers.RemoteServer{{
			ExternalID: "new-remote", Scope: "zone-a", Name: "new-server", State: providers.StateRunning,
			RemoteState: "RUNNING", Spec: json.RawMessage(`{}`), Addresses: json.RawMessage(`[]`),
		}}, Next: &providers.Cursor{Value: "next"}}, nil
	}
	return providers.ServerPage{}, &providers.Error{Code: providers.ErrorNetwork, Message: "second page failed", Retryable: true}
}
func (*scriptedProvider) GetServer(context.Context, providers.ServerRef) (providers.RemoteServer, error) {
	return providers.RemoteServer{}, errors.New("unused")
}
func (*scriptedProvider) StartServer(context.Context, providers.ServerRef) (providers.ActionReceipt, error) {
	return providers.ActionReceipt{}, errors.New("unused")
}
func (*scriptedProvider) StopServer(context.Context, providers.ServerRef) (providers.ActionReceipt, error) {
	return providers.ActionReceipt{}, errors.New("unused")
}
func (*scriptedProvider) RebootServer(context.Context, providers.ServerRef) (providers.ActionReceipt, error) {
	return providers.ActionReceipt{}, errors.New("unused")
}
func (*scriptedProvider) OpenConsole(context.Context, providers.ServerRef, providers.ConsoleMode) (providers.ConsoleTarget, error) {
	return providers.ConsoleTarget{}, errors.New("unused")
}
func (*scriptedProvider) ProviderPortalURL(context.Context, providers.ServerRef) (*url.URL, error) {
	return nil, &providers.Error{Code: providers.ErrorUnsupported, Message: "unavailable"}
}
