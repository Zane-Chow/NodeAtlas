package jobs

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
		URL: "sqlite://" + filepath.Join(t.TempDir(), "jobs.db"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, database.Migrate(context.Background(), db, dialect))
	runRepositoryContract(t, NewSQLRepository(db, dialect))
}

func runRepositoryContract(t *testing.T, repository Repository) {
	ctx := context.Background()
	now := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	job := Job{
		ID: "job-a", Kind: KindSyncConnection, Payload: json.RawMessage(`{"connection_id":"connection-a"}`),
		Status: StatusQueued, MaxAttempts: 3, AvailableAt: now, CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, repository.Enqueue(ctx, job))

	first, err := repository.LeaseNext(ctx, "worker-a", now, time.Minute)
	require.NoError(t, err)
	require.Equal(t, "worker-a", first.Owner)
	require.Equal(t, 1, first.Job.Attempts)
	_, err = repository.LeaseNext(ctx, "worker-b", now.Add(30*time.Second), time.Minute)
	require.ErrorIs(t, err, ErrNoJob)

	reclaimed, err := repository.LeaseNext(ctx, "worker-b", now.Add(2*time.Minute), time.Minute)
	require.NoError(t, err)
	require.Equal(t, "worker-b", reclaimed.Owner)
	require.Equal(t, 2, reclaimed.Job.Attempts)

	nextAttempt := now.Add(5 * time.Minute)
	require.NoError(t, repository.Retry(ctx, job.ID, "worker-b", nextAttempt, "temporary network error"))
	_, err = repository.LeaseNext(ctx, "worker-c", now.Add(4*time.Minute), time.Minute)
	require.ErrorIs(t, err, ErrNoJob)
	third, err := repository.LeaseNext(ctx, "worker-c", nextAttempt, time.Minute)
	require.NoError(t, err)
	require.Equal(t, 3, third.Job.Attempts)
	require.NoError(t, repository.Complete(ctx, job.ID, "worker-c", nextAttempt.Add(time.Second)))

	stored, err := repository.FindByID(ctx, job.ID)
	require.NoError(t, err)
	require.Equal(t, StatusSucceeded, stored.Status)
	require.Equal(t, 3, stored.Attempts)
	require.Equal(t, "temporary network error", stored.LastError)
}

func TestRepositoryFailsJobAtAttemptLimit(t *testing.T) {
	db, dialect, err := database.Open(context.Background(), config.DatabaseConfig{
		URL: "sqlite://" + filepath.Join(t.TempDir(), "jobs.db"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, database.Migrate(context.Background(), db, dialect))
	repository := NewSQLRepository(db, dialect)
	now := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	require.NoError(t, repository.Enqueue(context.Background(), Job{
		ID: "job-limit", Kind: KindSyncConnection, Payload: json.RawMessage(`{}`), Status: StatusQueued,
		MaxAttempts: 1, AvailableAt: now, CreatedAt: now, UpdatedAt: now,
	}))
	_, err = repository.LeaseNext(context.Background(), "worker", now, time.Minute)
	require.NoError(t, err)
	require.NoError(t, repository.Retry(context.Background(), "job-limit", "worker", now.Add(time.Minute), "failed"))
	stored, err := repository.FindByID(context.Background(), "job-limit")
	require.NoError(t, err)
	require.Equal(t, StatusFailed, stored.Status)
}
