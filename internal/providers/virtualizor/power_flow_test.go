package virtualizor

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"controlpanel/internal/audit"
	"controlpanel/internal/config"
	"controlpanel/internal/connections"
	"controlpanel/internal/database"
	"controlpanel/internal/inventory"
	"controlpanel/internal/jobs"
	"controlpanel/internal/operations"
	"controlpanel/internal/providers"
	"controlpanel/internal/secrets"
	"github.com/stretchr/testify/require"
)

type fixtureFactory struct{ provider providers.Provider }

func (factory fixtureFactory) Create(providers.ConnectionConfig) (providers.Provider, error) {
	return factory.provider, nil
}

func TestPowerExecutorUsesVirtualizorInfoStatus(t *testing.T) {
	for _, action := range []operations.Action{operations.ActionStart, operations.ActionStop} {
		t.Run(string(action), func(t *testing.T) {
			initial, target := 0, 1
			state := inventory.StateRunning
			if action == operations.ActionStop {
				initial, target = 1, 0
				state = inventory.StateStopped
			}
			var writes, lookups atomic.Int32
			p := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Query().Get("act") {
				case "listvs":
					writeJSON(t, w, map[string]any{"3332": map[string]any{"vpsid": 3332, "status": initial}})
				case "vpsmanage":
					status := initial
					if lookups.Add(1) >= 3 && writes.Load() > 0 {
						status = target
					}
					writeJSON(t, w, map[string]any{"info": map[string]any{"status": status, "vps": map[string]any{"vpsid": 3332}}})
				case string(action):
					writes.Add(1)
					require.Equal(t, "3332", r.URL.Query().Get("svs"))
					require.Equal(t, "1", r.URL.Query().Get("do"))
					writeJSON(t, w, map[string]any{"done": map[string]any{"msg": "accepted"}})
				default:
					t.Error("unexpected power flow action")
				}
			})
			ctx := context.Background()
			db, dialect, err := database.Open(ctx, config.DatabaseConfig{URL: "sqlite://" + filepath.Join(t.TempDir(), "power.db")})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			require.NoError(t, database.Migrate(ctx, db, dialect))
			cipher, err := secrets.NewCredentialCipher(map[int][]byte{1: bytes.Repeat([]byte{8}, 32)}, 1)
			require.NoError(t, err)
			envelope, err := cipher.Encrypt("connection-a", "virtualizor", fixtureConfig(p.panelURL.String()).Credentials)
			require.NoError(t, err)
			now := time.Now().UTC()
			connectionRepository := connections.NewSQLRepository(db, dialect)
			require.NoError(t, connectionRepository.Create(ctx, connections.Connection{
				ID: "connection-a", Name: "Fixture", ProviderType: "virtualizor", Endpoint: p.panelURL.String(), Settings: json.RawMessage(`{}`),
				Enabled: true, HealthStatus: connections.HealthHealthy, CreatedAt: now, UpdatedAt: now,
			}, connections.CredentialRecord{Ciphertext: envelope.Ciphertext, Nonce: envelope.Nonce, KeyVersion: envelope.KeyVersion}))
			registry := providers.NewRegistry()
			require.NoError(t, registry.Register("virtualizor", fixtureFactory{provider: p}))
			inventoryRepository := inventory.NewSQLRepository(db, dialect)
			syncer := inventory.NewSyncer(connectionRepository, inventoryRepository, cipher, registry, inventory.SyncerOptions{NewID: func() string { return "server-a" }})
			require.NoError(t, syncer.SyncConnection(ctx, "connection-a"))
			operationRepository := operations.NewSQLRepository(db, dialect)
			_, created, err := operationRepository.CreateQueued(ctx, operations.Operation{
				ID: "operation-a", ServerID: "server-a", ConnectionID: "connection-a", Action: action,
				IdempotencyKey: "fixture-operation", QueuedAt: now, UpdatedAt: now,
			})
			require.NoError(t, err)
			require.True(t, created)
			executor := operations.NewExecutor(operationRepository, inventoryRepository, connectionRepository, audit.NewSQLRepository(db, dialect), cipher, registry, operations.ExecutorOptions{
				PollInterval: time.Millisecond, VerificationTimeout: 250 * time.Millisecond,
			})
			require.NoError(t, executor.Execute(ctx, jobs.Job{Kind: jobs.KindPowerOperation, Payload: json.RawMessage(`{"operation_id":"operation-a"}`), Attempts: 1, MaxAttempts: 1}))
			require.EqualValues(t, 1, writes.Load(), "power action must reach the upstream API")
			require.GreaterOrEqual(t, lookups.Load(), int32(3), "must poll until info.status confirms completion")
			operation, err := operationRepository.FindByID(ctx, "operation-a")
			require.NoError(t, err)
			require.Equal(t, operations.StatusSucceeded, operation.Status)
			server, err := inventoryRepository.FindByID(ctx, "server-a")
			require.NoError(t, err)
			require.Equal(t, state, server.State)
		})
	}
}
