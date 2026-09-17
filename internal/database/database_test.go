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

	for _, table := range []string{"schema_migrations", "users", "sessions", "provider_connections", "servers", "jobs", "operations", "audit_logs", "console_sessions", "backups", "settings"} {
		var count int
		err := db.QueryRow(
			"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?",
			table,
		).Scan(&count)
		require.NoError(t, err)
		require.Equal(t, 1, count, fmt.Sprintf("table %s should exist", table))
	}
}

func TestSQLiteMilestoneTwoConstraints(t *testing.T) {
	db, dialect, err := Open(context.Background(), config.DatabaseConfig{
		URL: "sqlite://" + filepath.Join(t.TempDir(), "controlpanel.db"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, Migrate(context.Background(), db, dialect))

	now := "2026-09-18T00:00:00Z"
	_, err = db.Exec(`INSERT INTO provider_connections
		(id, name, provider_type, settings_json, credentials_ciphertext, credentials_nonce,
		 credentials_key_version, enabled, health_status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"connection-a", "Lab", "mock", `{}`, []byte("ciphertext"), make([]byte, 12), 1, 1, "unknown", now, now)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO servers
		(id, connection_id, external_id, scope, name, normalized_state, remote_state,
		 spec_json, addresses_json, capabilities_json, last_seen_at, last_state_checked_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"server-a", "connection-a", "vm-1", "zone-a", "VM", "running", "RUNNING",
		`{}`, `[]`, `{}`, now, now, now, now)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO servers
		(id, connection_id, external_id, scope, name, normalized_state, remote_state,
		 spec_json, addresses_json, capabilities_json, last_seen_at, last_state_checked_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"server-b", "connection-a", "vm-1", "zone-a", "Duplicate", "running", "RUNNING",
		`{}`, `[]`, `{}`, now, now, now, now)
	require.Error(t, err)
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
