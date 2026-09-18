package backup

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"controlpanel/internal/config"
	"controlpanel/internal/database"
	"github.com/stretchr/testify/require"
)

func TestLogicalSnapshotExportsAndRestoresSQLite(t *testing.T) {
	db, dialect, err := database.Open(context.Background(), config.DatabaseConfig{URL: "sqlite://" + filepath.Join(t.TempDir(), "snapshot.db")})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, database.Migrate(context.Background(), db, dialect))
	now := "2026-09-18T16:00:00Z"
	_, err = db.Exec(`INSERT INTO users
		(singleton_key, id, username, normalized_username, password_hash, initialized_at, created_at, updated_at)
		VALUES (1, 'user-a', 'Admin', 'admin', 'argon-hash', ?, ?, ?)`, now, now, now)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO settings (setting_key, value_json, updated_at) VALUES ('sync_interval', '{"minutes":15}', ?)`, now)
	require.NoError(t, err)

	snapshotter := NewSnapshotter(db, dialect, SnapshotOptions{ApplicationVersion: "test", Now: func() time.Time { return time.Date(2026, 9, 18, 16, 1, 0, 0, time.UTC) }})
	archive, err := snapshotter.Export(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, archive.Manifest["users"])
	require.Equal(t, 1, archive.Manifest["settings"])
	require.NotContains(t, archive.Tables, "backups")

	_, err = db.Exec(`UPDATE users SET username = 'Changed', normalized_username = 'changed' WHERE id = 'user-a'`)
	require.NoError(t, err)
	_, err = db.Exec(`DELETE FROM settings`)
	require.NoError(t, err)
	require.NoError(t, snapshotter.Restore(context.Background(), archive))

	var username string
	require.NoError(t, db.QueryRow(`SELECT username FROM users WHERE id = 'user-a'`).Scan(&username))
	require.Equal(t, "Admin", username)
	var setting string
	require.NoError(t, db.QueryRow(`SELECT value_json FROM settings WHERE setting_key = 'sync_interval'`).Scan(&setting))
	require.JSONEq(t, `{"minutes":15}`, setting)
}

func TestLogicalSnapshotRejectsUnknownOrMalformedTablesBeforeMutation(t *testing.T) {
	db, dialect, err := database.Open(context.Background(), config.DatabaseConfig{URL: "sqlite://" + filepath.Join(t.TempDir(), "invalid.db")})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, database.Migrate(context.Background(), db, dialect))
	snapshotter := NewSnapshotter(db, dialect, SnapshotOptions{})
	archive, err := snapshotter.Export(context.Background())
	require.NoError(t, err)
	archive.Tables["unknown"] = TableData{}
	archive.Manifest["unknown"] = 0
	require.ErrorIs(t, snapshotter.Restore(context.Background(), archive), ErrInvalidManifest)
}
