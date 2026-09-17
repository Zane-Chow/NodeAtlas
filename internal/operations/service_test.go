package operations

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"controlpanel/internal/audit"
	"controlpanel/internal/config"
	"controlpanel/internal/connections"
	"controlpanel/internal/database"
	"controlpanel/internal/inventory"
	"controlpanel/internal/providers"
	"github.com/stretchr/testify/require"
)

type recordingPowerQueue struct{ operationIDs []string }

func (queue *recordingPowerQueue) EnqueuePowerOperation(_ context.Context, operationID string) error {
	queue.operationIDs = append(queue.operationIDs, operationID)
	return nil
}

type recordingAudit struct{ entries []audit.Entry }

func (repository *recordingAudit) Append(_ context.Context, entry audit.Entry) error {
	repository.entries = append(repository.entries, entry)
	return nil
}

func TestServiceRequestsOneIdempotentPowerOperation(t *testing.T) {
	service, _, _, queue, auditLog := newOperationServiceFixture(t, inventory.StateStopped, providers.Capabilities{
		CanStart: providers.Capability{Available: true},
	})

	first, created, err := service.Request(context.Background(), Request{
		ServerID: "server-a", Action: ActionStart, IdempotencyKey: "idem-a", RequestID: "request-a", SourceIP: "192.0.2.10",
	})
	require.NoError(t, err)
	require.True(t, created)
	replayed, created, err := service.Request(context.Background(), Request{
		ServerID: "server-a", Action: ActionStart, IdempotencyKey: "idem-a", RequestID: "request-b", SourceIP: "192.0.2.11",
	})
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, first.ID, replayed.ID)
	require.Equal(t, []string{"operation-a"}, queue.operationIDs)
	require.Len(t, auditLog.entries, 1)
	require.Equal(t, "power_operation_queued", auditLog.entries[0].EventType)
	require.Equal(t, "request-a", auditLog.entries[0].RequestID)
	require.JSONEq(t, `{"action":"start","status":"queued"}`, string(auditLog.entries[0].Metadata))
}

func TestServiceReplaysCompletedOperationAfterServerStateChanges(t *testing.T) {
	service, repository, inventoryRepository, queue, _ := newOperationServiceFixture(t, inventory.StateStopped, providers.Capabilities{
		CanStart: providers.Capability{Available: true},
	})
	first, _, err := service.Request(context.Background(), Request{ServerID: "server-a", Action: ActionStart, IdempotencyKey: "idem-a"})
	require.NoError(t, err)
	_, err = repository.Transition(context.Background(), first.ID, StatusRunning, Transition{At: first.QueuedAt.Add(time.Second)})
	require.NoError(t, err)
	_, err = repository.Transition(context.Background(), first.ID, StatusSucceeded, Transition{At: first.QueuedAt.Add(2 * time.Second)})
	require.NoError(t, err)
	runningCapabilities, err := json.Marshal(providers.Capabilities{CanStart: providers.Capability{Available: false}})
	require.NoError(t, err)
	require.NoError(t, inventoryRepository.ApplyCompleteSync(context.Background(), inventory.SyncSnapshot{
		ConnectionID: "connection-a", CompletedAt: first.QueuedAt.Add(3 * time.Second), Servers: []inventory.Server{{
			ID: "server-a", ConnectionID: "connection-a", ExternalID: "remote-a", Scope: "zone-a", Name: "api-01",
			State: inventory.StateRunning, RemoteState: "RUNNING", Spec: json.RawMessage(`{}`), Addresses: json.RawMessage(`[]`),
			Capabilities: runningCapabilities, LastSeenAt: first.QueuedAt.Add(3 * time.Second), LastStateCheckedAt: first.QueuedAt.Add(3 * time.Second),
			CreatedAt: first.QueuedAt, UpdatedAt: first.QueuedAt.Add(3 * time.Second),
		}},
	}))

	replayed, created, err := service.Request(context.Background(), Request{ServerID: "server-a", Action: ActionStart, IdempotencyKey: "idem-a"})
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, first.ID, replayed.ID)
	require.Equal(t, []string{"operation-a"}, queue.operationIDs)
}

func TestServiceRejectsUnavailableActionAndStateConflict(t *testing.T) {
	service, repository, _, queue, _ := newOperationServiceFixture(t, inventory.StateRunning, providers.Capabilities{
		CanStart: providers.Capability{Available: false, Reason: "server must be stopped"},
		CanStop:  providers.Capability{Available: true},
	})

	_, _, err := service.Request(context.Background(), Request{ServerID: "server-a", Action: ActionStart, IdempotencyKey: "idem-a"})
	require.ErrorIs(t, err, ErrStateConflict)
	_, _, err = service.Request(context.Background(), Request{ServerID: "server-a", Action: Action("destroy"), IdempotencyKey: "idem-b"})
	require.ErrorIs(t, err, ErrInvalidAction)
	listed, listErr := repository.List(context.Background(), Filter{})
	require.NoError(t, listErr)
	require.Empty(t, listed)
	require.Empty(t, queue.operationIDs)
}

func TestServiceRejectsSecondActiveOperation(t *testing.T) {
	service, _, _, _, _ := newOperationServiceFixture(t, inventory.StateRunning, providers.Capabilities{
		CanStop: providers.Capability{Available: true}, CanReboot: providers.Capability{Available: true},
	})
	_, created, err := service.Request(context.Background(), Request{ServerID: "server-a", Action: ActionStop, IdempotencyKey: "idem-a"})
	require.NoError(t, err)
	require.True(t, created)
	_, _, err = service.Request(context.Background(), Request{ServerID: "server-a", Action: ActionReboot, IdempotencyKey: "idem-b"})
	require.ErrorIs(t, err, ErrActiveOperation)
}

func newOperationServiceFixture(t *testing.T, state inventory.State, capabilities providers.Capabilities) (*Service, *SQLRepository, *inventory.SQLRepository, *recordingPowerQueue, *recordingAudit) {
	t.Helper()
	db, dialect, err := database.Open(context.Background(), config.DatabaseConfig{
		URL: "sqlite://" + filepath.Join(t.TempDir(), "operation-service.db"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, database.Migrate(context.Background(), db, dialect))
	now := time.Date(2026, 9, 18, 11, 0, 0, 0, time.UTC)
	require.NoError(t, connections.NewSQLRepository(db, dialect).Create(context.Background(), connections.Connection{
		ID: "connection-a", Name: "Lab", ProviderType: "mock", Settings: json.RawMessage(`{}`),
		Enabled: true, HealthStatus: connections.HealthHealthy, CreatedAt: now, UpdatedAt: now,
	}, connections.CredentialRecord{Ciphertext: []byte("cipher"), Nonce: make([]byte, 12), KeyVersion: 1}))
	encodedCapabilities, err := json.Marshal(capabilities)
	require.NoError(t, err)
	inventoryRepository := inventory.NewSQLRepository(db, dialect)
	require.NoError(t, inventoryRepository.ApplyCompleteSync(context.Background(), inventory.SyncSnapshot{
		ConnectionID: "connection-a", CompletedAt: now, Servers: []inventory.Server{{
			ID: "server-a", ConnectionID: "connection-a", ExternalID: "remote-a", Scope: "zone-a", Name: "api-01",
			State: state, RemoteState: string(state), Spec: json.RawMessage(`{}`), Addresses: json.RawMessage(`[]`),
			Capabilities: encodedCapabilities, LastSeenAt: now, LastStateCheckedAt: now, CreatedAt: now, UpdatedAt: now,
		}},
	}))
	repository := NewSQLRepository(db, dialect)
	queue := &recordingPowerQueue{}
	auditLog := &recordingAudit{}
	service := NewService(repository, inventoryRepository, queue, auditLog, ServiceOptions{
		Now: func() time.Time { return now }, NewID: func() string { return "operation-a" },
		NewIdempotencyKey: func() string { return "generated-idempotency" },
	})
	return service, repository, inventoryRepository, queue, auditLog
}
