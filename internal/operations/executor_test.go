package operations

import (
	"bytes"
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
	"controlpanel/internal/jobs"
	"controlpanel/internal/providers"
	providermock "controlpanel/internal/providers/mock"
	"controlpanel/internal/secrets"
	"github.com/stretchr/testify/require"
)

func TestExecutorStartsAndVerifiesRemoteServer(t *testing.T) {
	fixture := newExecutorFixture(t, ActionStart, `{"server_count":1,"seed":1}`)
	err := fixture.executor.Execute(context.Background(), fixture.job(1, 3))
	require.NoError(t, err)
	operation, err := fixture.operations.FindByID(context.Background(), "operation-a")
	require.NoError(t, err)
	require.Equal(t, StatusSucceeded, operation.Status)
	require.NotEmpty(t, operation.ProviderRequestID)
	server, err := fixture.inventory.FindByID(context.Background(), "server-a")
	require.NoError(t, err)
	require.Equal(t, inventory.StateRunning, server.State)
	entries, err := fixture.audit.List(context.Background(), audit.Filter{TargetID: "operation-a"})
	require.NoError(t, err)
	require.Equal(t, "power_operation_succeeded", entries[0].EventType)
}

func TestExecutorSkipsStartWhenRemoteAlreadyRunning(t *testing.T) {
	fixture := newExecutorFixture(t, ActionStart, `{"server_count":1,"seed":4}`)
	err := fixture.executor.Execute(context.Background(), fixture.job(1, 3))
	require.NoError(t, err)
	operation, err := fixture.operations.FindByID(context.Background(), "operation-a")
	require.NoError(t, err)
	require.Equal(t, StatusSucceeded, operation.Status)
	require.Empty(t, operation.ProviderRequestID)
}

func TestExecutorAlwaysIssuesRebootBeforeVerifyingRunningState(t *testing.T) {
	fixture := newExecutorFixture(t, ActionReboot, `{"server_count":1,"seed":4}`)
	err := fixture.executor.Execute(context.Background(), fixture.job(1, 3))
	require.NoError(t, err)
	operation, err := fixture.operations.FindByID(context.Background(), "operation-a")
	require.NoError(t, err)
	require.Equal(t, StatusSucceeded, operation.Status)
	require.NotEmpty(t, operation.ProviderRequestID)
}

func TestExecutorReturnsRetryableErrorAndFailsAtAttemptLimit(t *testing.T) {
	fixture := newExecutorFixture(t, ActionStart, `{"server_count":1,"seed":1,"failure_rate":1}`)
	err := fixture.executor.Execute(context.Background(), fixture.job(1, 2))
	require.Error(t, err)
	var providerError *providers.Error
	require.ErrorAs(t, err, &providerError)
	require.True(t, providerError.Retryable)
	operation, findErr := fixture.operations.FindByID(context.Background(), "operation-a")
	require.NoError(t, findErr)
	require.Equal(t, StatusRunning, operation.Status)

	err = fixture.executor.Execute(context.Background(), fixture.job(2, 2))
	require.Error(t, err)
	operation, findErr = fixture.operations.FindByID(context.Background(), "operation-a")
	require.NoError(t, findErr)
	require.Equal(t, StatusFailed, operation.Status)
	require.Equal(t, string(providers.ErrorProvider), operation.ErrorCode)
}

func TestExecutorFailsAuthenticationWithoutProviderWrite(t *testing.T) {
	fixture := newExecutorFixture(t, ActionStart, `{"server_count":1,"seed":1,"health_mode":"authentication_failure"}`)
	err := fixture.executor.Execute(context.Background(), fixture.job(1, 3))
	require.Error(t, err)
	operation, findErr := fixture.operations.FindByID(context.Background(), "operation-a")
	require.NoError(t, findErr)
	require.Equal(t, StatusFailed, operation.Status)
	require.Equal(t, string(providers.ErrorAuthentication), operation.ErrorCode)
	require.Empty(t, operation.ProviderRequestID)
}

type executorFixture struct {
	executor   *Executor
	operations *SQLRepository
	inventory  *inventory.SQLRepository
	audit      *audit.SQLRepository
}

func newExecutorFixture(t *testing.T, action Action, settings string) executorFixture {
	t.Helper()
	db, dialect, err := database.Open(context.Background(), config.DatabaseConfig{
		URL: "sqlite://" + filepath.Join(t.TempDir(), "executor.db"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, database.Migrate(context.Background(), db, dialect))
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	cipher, err := secrets.NewCredentialCipher(map[int][]byte{1: bytes.Repeat([]byte{8}, 32)}, 1)
	require.NoError(t, err)
	plaintext := json.RawMessage(`{"token":"executor-secret"}`)
	envelope, err := cipher.Encrypt("connection-a", "mock", plaintext)
	require.NoError(t, err)
	connectionRepository := connections.NewSQLRepository(db, dialect)
	connectionSettings := json.RawMessage(settings)
	initialSettings := connectionSettings
	if settings == `{"server_count":1,"seed":1,"health_mode":"authentication_failure"}` {
		initialSettings = json.RawMessage(`{"server_count":1,"seed":1}`)
	}
	require.NoError(t, connectionRepository.Create(context.Background(), connections.Connection{
		ID: "connection-a", Name: "Lab", ProviderType: "mock", Settings: initialSettings, Enabled: true,
		HealthStatus: connections.HealthHealthy, CreatedAt: now, UpdatedAt: now,
	}, connections.CredentialRecord{Ciphertext: envelope.Ciphertext, Nonce: envelope.Nonce, KeyVersion: envelope.KeyVersion}))
	registry := providers.NewRegistry()
	require.NoError(t, registry.Register("mock", providermock.NewFactory()))
	inventoryRepository := inventory.NewSQLRepository(db, dialect)
	syncer := inventory.NewSyncer(connectionRepository, inventoryRepository, cipher, registry, inventory.SyncerOptions{
		Now: func() time.Time { return now }, NewID: func() string { return "server-a" },
	})
	require.NoError(t, syncer.SyncConnection(context.Background(), "connection-a"))
	if string(initialSettings) != string(connectionSettings) {
		connection, _, err := connectionRepository.FindByID(context.Background(), "connection-a")
		require.NoError(t, err)
		connection.Settings = connectionSettings
		require.NoError(t, connectionRepository.Update(context.Background(), connection, nil))
	}
	operationRepository := NewSQLRepository(db, dialect)
	_, created, err := operationRepository.CreateQueued(context.Background(), Operation{
		ID: "operation-a", ServerID: "server-a", ConnectionID: "connection-a", Action: action,
		IdempotencyKey: "idem-executor", QueuedAt: now, UpdatedAt: now,
	})
	require.NoError(t, err)
	require.True(t, created)
	auditRepository := audit.NewSQLRepository(db, dialect)
	executor := NewExecutor(operationRepository, inventoryRepository, connectionRepository, auditRepository, cipher, registry, ExecutorOptions{
		Now: func() time.Time { return now.Add(time.Minute) }, PollInterval: time.Millisecond, VerificationTimeout: 20 * time.Millisecond,
		NewAuditID: func() string { return "audit-operation" },
	})
	return executorFixture{executor: executor, operations: operationRepository, inventory: inventoryRepository, audit: auditRepository}
}

func (fixture executorFixture) job(attempts, maxAttempts int) jobs.Job {
	return jobs.Job{
		ID: "job-operation", Kind: jobs.KindPowerOperation, Payload: json.RawMessage(`{"operation_id":"operation-a"}`),
		Status: jobs.StatusRunning, Attempts: attempts, MaxAttempts: maxAttempts,
	}
}
