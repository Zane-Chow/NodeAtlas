package backup

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

func TestSQLiteRepositoryStoresSafeBackupMetadataNewestFirst(t *testing.T) {
	db, dialect, err := database.Open(context.Background(), config.DatabaseConfig{URL: "sqlite://" + filepath.Join(t.TempDir(), "backup.db")})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, database.Migrate(context.Background(), db, dialect))
	repository := NewSQLRepository(db, dialect)
	now := time.Date(2026, 9, 18, 13, 0, 0, 0, time.UTC)
	for _, item := range []Metadata{
		{ID: "backup-a", Filename: "backup-a.scpb", SizeBytes: 42, SHA256: "aaa", FormatVersion: 1, Kind: KindManual, Status: StatusReady, Manifest: json.RawMessage(`{"users":1}`), CreatedAt: now},
		{ID: "backup-b", Filename: "backup-b.scpb", SizeBytes: 84, SHA256: "bbb", FormatVersion: 1, Kind: KindSafety, Status: StatusReady, Manifest: json.RawMessage(`{"users":1}`), CreatedAt: now.Add(time.Second)},
	} {
		require.NoError(t, repository.Create(context.Background(), item))
	}

	listed, err := repository.List(context.Background())
	require.NoError(t, err)
	require.Len(t, listed, 2)
	require.Equal(t, "backup-b", listed[0].ID)
	found, err := repository.FindByID(context.Background(), "backup-a")
	require.NoError(t, err)
	require.Equal(t, int64(42), found.SizeBytes)
	serialized, err := json.Marshal(listed)
	require.NoError(t, err)
	require.NotContains(t, string(serialized), "passphrase")
	require.NotContains(t, string(serialized), "path")
}
