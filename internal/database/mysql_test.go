package database

import (
	"context"
	"os"
	"testing"

	"controlpanel/internal/config"
	"github.com/stretchr/testify/require"
)

func TestMySQLMigrationsAreIdempotent(t *testing.T) {
	databaseURL := os.Getenv("TEST_MYSQL_URL")
	if databaseURL == "" {
		t.Skip("TEST_MYSQL_URL is not configured")
	}
	db, dialect, err := Open(context.Background(), config.DatabaseConfig{URL: databaseURL})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.Equal(t, DialectMySQL, dialect)
	require.NoError(t, Migrate(context.Background(), db, dialect))
	require.NoError(t, Migrate(context.Background(), db, dialect))

	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version = 1").Scan(&count))
	require.Equal(t, 1, count)
}
