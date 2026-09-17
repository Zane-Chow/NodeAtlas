package audit

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

func TestSQLiteRepositoryAppendsAndFiltersImmutableAuditEntries(t *testing.T) {
	db, dialect, err := database.Open(context.Background(), config.DatabaseConfig{
		URL: "sqlite://" + filepath.Join(t.TempDir(), "audit.db"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, database.Migrate(context.Background(), db, dialect))
	repository := NewSQLRepository(db, dialect)
	now := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	require.NoError(t, repository.Append(context.Background(), Entry{
		ID: "audit-a", EventType: "power_operation_queued", TargetType: "operation", TargetID: "operation-a",
		RequestID: "request-a", SourceIP: "192.0.2.10", Metadata: json.RawMessage(`{"action":"start"}`), CreatedAt: now,
	}))
	require.NoError(t, repository.Append(context.Background(), Entry{
		ID: "audit-b", EventType: "login_succeeded", TargetType: "user", TargetID: "user-a",
		RequestID: "request-b", SourceIP: "192.0.2.11", Metadata: json.RawMessage(`{}`), CreatedAt: now.Add(time.Second),
	}))

	entries, err := repository.List(context.Background(), Filter{TargetType: "operation", TargetID: "operation-a"})
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "audit-a", entries[0].ID)
	require.JSONEq(t, `{"action":"start"}`, string(entries[0].Metadata))
}
