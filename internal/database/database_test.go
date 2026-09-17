package database

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"controlpanel/internal/config"
	"github.com/stretchr/testify/require"
)

func TestSQLiteMigrationsAreIdempotent(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "controlpanel.db")
	db, dialect, err := Open(context.Background(), config.DatabaseConfig{
		URL: "sqlite://" + databasePath,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.Equal(t, DialectSQLite, dialect)

	require.NoError(t, Migrate(context.Background(), db, dialect))
	require.NoError(t, Migrate(context.Background(), db, dialect))

	for _, table := range []string{"schema_migrations", "users", "sessions"} {
		var count int
		err := db.QueryRow(
			"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?",
			table,
		).Scan(&count)
		require.NoError(t, err)
		require.Equal(t, 1, count, fmt.Sprintf("table %s should exist", table))
	}
}

func TestSQLiteEnforcesForeignKeysAndSingleAdministrator(t *testing.T) {
	db, dialect, err := Open(context.Background(), config.DatabaseConfig{
		URL: "sqlite://" + filepath.Join(t.TempDir(), "controlpanel.db"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, Migrate(context.Background(), db, dialect))

	var foreignKeys int
	require.NoError(t, db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys))
	require.Equal(t, 1, foreignKeys)

	_, err = db.Exec(`INSERT INTO users
		(singleton_key, id, username, normalized_username, password_hash, initialized_at, created_at, updated_at)
		VALUES (1, 'user-1', 'Admin', 'admin', 'hash', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO users
		(singleton_key, id, username, normalized_username, password_hash, initialized_at, created_at, updated_at)
		VALUES (1, 'user-2', 'Other', 'other', 'hash', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	require.Error(t, err)
}
