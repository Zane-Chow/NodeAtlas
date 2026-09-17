package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"controlpanel/internal/config"
	"controlpanel/internal/database"
	"controlpanel/internal/providers"
	"github.com/stretchr/testify/require"
)

func TestQueuePersistsConnectionSyncJob(t *testing.T) {
	repository, now := newWorkerRepository(t)
	queue := NewQueue(repository, QueueOptions{
		Now: func() time.Time { return now }, NewID: func() string { return "sync-job" },
	})

	require.NoError(t, queue.EnqueueConnectionSync(context.Background(), "connection-a"))
	stored, err := repository.FindByID(context.Background(), "sync-job")
	require.NoError(t, err)
	require.Equal(t, KindSyncConnection, stored.Kind)
	require.JSONEq(t, `{"connection_id":"connection-a"}`, string(stored.Payload))
	require.Equal(t, StatusQueued, stored.Status)
}

func TestWorkerCompletesSuccessfulJob(t *testing.T) {
	repository, now := newWorkerRepository(t)
	enqueueWorkerJob(t, repository, now, "success")
	worker := NewWorker(repository, HandlerFunc(func(context.Context, Job) error { return nil }), WorkerOptions{
		Owner: "worker-a", Now: func() time.Time { return now }, LeaseDuration: time.Minute,
	})

	require.NoError(t, worker.RunOnce(context.Background()))
	stored, err := repository.FindByID(context.Background(), "success")
	require.NoError(t, err)
	require.Equal(t, StatusSucceeded, stored.Status)
}

func TestWorkerRetriesRetryableFailure(t *testing.T) {
	repository, now := newWorkerRepository(t)
	enqueueWorkerJob(t, repository, now, "retry")
	worker := NewWorker(repository, HandlerFunc(func(context.Context, Job) error {
		return &providers.Error{Code: providers.ErrorNetwork, Message: "network unavailable", Retryable: true}
	}), WorkerOptions{
		Owner: "worker-a", Now: func() time.Time { return now }, LeaseDuration: time.Minute,
		RetryDelay: func(int) time.Duration { return 30 * time.Second },
	})

	require.NoError(t, worker.RunOnce(context.Background()))
	stored, err := repository.FindByID(context.Background(), "retry")
	require.NoError(t, err)
	require.Equal(t, StatusQueued, stored.Status)
	require.Equal(t, now.Add(30*time.Second), stored.AvailableAt)
	require.Equal(t, "network unavailable", stored.LastError)
}

func TestWorkerFailsTerminalError(t *testing.T) {
	repository, now := newWorkerRepository(t)
	enqueueWorkerJob(t, repository, now, "terminal")
	worker := NewWorker(repository, HandlerFunc(func(context.Context, Job) error {
		return errors.New("invalid credentials")
	}), WorkerOptions{Owner: "worker-a", Now: func() time.Time { return now }, LeaseDuration: time.Minute})

	require.NoError(t, worker.RunOnce(context.Background()))
	stored, err := repository.FindByID(context.Background(), "terminal")
	require.NoError(t, err)
	require.Equal(t, StatusFailed, stored.Status)
	require.Equal(t, "invalid credentials", stored.LastError)
}

func newWorkerRepository(t *testing.T) (*SQLRepository, time.Time) {
	t.Helper()
	db, dialect, err := database.Open(context.Background(), config.DatabaseConfig{URL: "sqlite://" + filepath.Join(t.TempDir(), "worker.db")})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, database.Migrate(context.Background(), db, dialect))
	return NewSQLRepository(db, dialect), time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
}

func enqueueWorkerJob(t *testing.T, repository Repository, now time.Time, id string) {
	t.Helper()
	require.NoError(t, repository.Enqueue(context.Background(), Job{
		ID: id, Kind: KindSyncConnection, Payload: json.RawMessage(`{"connection_id":"connection-a"}`),
		Status: StatusQueued, MaxAttempts: 3, AvailableAt: now, CreatedAt: now, UpdatedAt: now,
	}))
}
