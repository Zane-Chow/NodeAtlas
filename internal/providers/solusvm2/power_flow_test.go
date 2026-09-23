package solusvm2

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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

func TestPowerExecutorSendsActionsAndPollsRealStatus(t *testing.T) {
	for _, action := range []operations.Action{operations.ActionStart, operations.ActionStop, operations.ActionReboot} {
		t.Run(string(action), func(t *testing.T) {
			initial, target, apiAction := "started", "started", "restart"
			wantState := inventory.StateRunning
			if action == operations.ActionStart {
				initial, apiAction = "stopped", "start"
			}
			if action == operations.ActionStop {
				target, apiAction, wantState = "stopped", "stop", inventory.StateStopped
			}
			var writes, polls atomic.Int32
			p := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/servers":
					writeJSON(t, w, fixturePage([]any{fixtureServer(1, initial)}, 1, 1))
				case "/api/v1/servers/1":
					state := initial
					if writes.Load() > 0 {
						state = "processing"
						if polls.Add(1) >= 2 {
							state = target
						}
					}
					writeJSON(t, w, map[string]any{"data": fixtureServer(1, state)})
				case "/api/v1/servers/1/" + apiAction:
					require.Equal(t, http.MethodPost, r.Method)
					raw, err := io.ReadAll(r.Body)
					require.NoError(t, err)
					if action == operations.ActionStart {
						require.Empty(t, raw)
					} else {
						require.JSONEq(t, `{"force":false}`, string(raw))
					}
					writes.Add(1)
					writeJSON(t, w, map[string]any{"data": map[string]any{"id": 987}})
				default:
					t.Error("unexpected power flow path")
				}
			})
			ctx := context.Background()
			db, dialect, err := database.Open(ctx, config.DatabaseConfig{URL: "sqlite://" + filepath.Join(t.TempDir(), "power.db")})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			require.NoError(t, database.Migrate(ctx, db, dialect))
			cipher, err := secrets.NewCredentialCipher(map[int][]byte{1: bytes.Repeat([]byte{8}, 32)}, 1)
			require.NoError(t, err)
			envelope, err := cipher.Encrypt("connection-a", "solusvm2", fixtureConfig(p.panelURL.String()).Credentials)
			require.NoError(t, err)
			now := time.Now().UTC()
			connectionsRepo := connections.NewSQLRepository(db, dialect)
			require.NoError(t, connectionsRepo.Create(ctx, connections.Connection{ID: "connection-a", Name: "Fixture", ProviderType: "solusvm2", Endpoint: p.panelURL.String(), Settings: json.RawMessage(`{}`), Enabled: true, HealthStatus: connections.HealthHealthy, CreatedAt: now, UpdatedAt: now}, connections.CredentialRecord{Ciphertext: envelope.Ciphertext, Nonce: envelope.Nonce, KeyVersion: envelope.KeyVersion}))
			registry := providers.NewRegistry()
			require.NoError(t, registry.Register("solusvm2", fixtureFactory{provider: p}))
			inventoryRepo := inventory.NewSQLRepository(db, dialect)
			syncer := inventory.NewSyncer(connectionsRepo, inventoryRepo, cipher, registry, inventory.SyncerOptions{NewID: func() string { return "server-a" }})
			require.NoError(t, syncer.SyncConnection(ctx, "connection-a"))
			operationsRepo := operations.NewSQLRepository(db, dialect)
			_, created, err := operationsRepo.CreateQueued(ctx, operations.Operation{ID: "operation-a", ServerID: "server-a", ConnectionID: "connection-a", Action: action, IdempotencyKey: "fixture-operation", QueuedAt: now, UpdatedAt: now})
			require.NoError(t, err)
			require.True(t, created)
			executor := operations.NewExecutor(operationsRepo, inventoryRepo, connectionsRepo, audit.NewSQLRepository(db, dialect), cipher, registry, operations.ExecutorOptions{PollInterval: time.Millisecond, VerificationTimeout: 500 * time.Millisecond})
			require.NoError(t, executor.Execute(ctx, jobs.Job{Kind: jobs.KindPowerOperation, Payload: json.RawMessage(`{"operation_id":"operation-a"}`), Attempts: 1, MaxAttempts: 1}))
			require.EqualValues(t, 1, writes.Load())
			require.GreaterOrEqual(t, polls.Load(), int32(2))
			op, err := operationsRepo.FindByID(ctx, "operation-a")
			require.NoError(t, err)
			require.Equal(t, operations.StatusSucceeded, op.Status)
			require.Equal(t, "987", op.ProviderRequestID)
			server, err := inventoryRepo.FindByID(ctx, "server-a")
			require.NoError(t, err)
			require.Equal(t, wantState, server.State)
		})
	}
}
