package jobs

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"controlpanel/internal/config"
	"controlpanel/internal/database"
	"github.com/stretchr/testify/require"
)

func TestQueueEnqueuesPowerOperationPayload(t *testing.T) {
	db, dialect, err := database.Open(context.Background(), config.DatabaseConfig{
		URL: "sqlite://" + filepath.Join(t.TempDir(), "queue.db"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, database.Migrate(context.Background(), db, dialect))
	repository := NewSQLRepository(db, dialect)
	now := time.Date(2026, 9, 18, 11, 0, 0, 0, time.UTC)
	queue := NewQueue(repository, QueueOptions{
		Now: func() time.Time { return now }, NewID: func() string { return "job-power-a" }, MaxAttempts: 4,
	})

	require.NoError(t, queue.EnqueuePowerOperation(context.Background(), "operation-a"))

	job, err := repository.FindByID(context.Background(), "job-power-a")
	require.NoError(t, err)
	require.Equal(t, KindPowerOperation, job.Kind)
	require.JSONEq(t, `{"operation_id":"operation-a"}`, string(job.Payload))
	require.Equal(t, 4, job.MaxAttempts)
}

func TestQueueRejectsEmptyPowerOperationID(t *testing.T) {
	queue := NewQueue(nil, QueueOptions{})
	require.Error(t, queue.EnqueuePowerOperation(context.Background(), ""))
}
