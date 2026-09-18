package backup

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"controlpanel/internal/audit"
	"controlpanel/internal/config"
	"controlpanel/internal/database"
	"github.com/stretchr/testify/require"
)

func TestServiceCreatesValidatesDownloadsAndRestoresBackup(t *testing.T) {
	db, dialect, err := database.Open(context.Background(), config.DatabaseConfig{URL: "sqlite://" + filepath.Join(t.TempDir(), "service.db")})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, database.Migrate(context.Background(), db, dialect))
	now := time.Date(2026, 9, 18, 17, 0, 0, 0, time.UTC)
	timestamp := now.Format(time.RFC3339Nano)
	_, err = db.Exec(`INSERT INTO users (singleton_key, id, username, normalized_username, password_hash, initialized_at, created_at, updated_at)
		VALUES (1, 'user-a', 'Admin', 'admin', 'argon-hash', ?, ?, ?)`, timestamp, timestamp, timestamp)
	require.NoError(t, err)
	directory := filepath.Join(t.TempDir(), "backups")
	repository := NewSQLRepository(db, dialect)
	service, err := NewService(repository, NewSnapshotter(db, dialect, SnapshotOptions{ApplicationVersion: "test", Now: func() time.Time { return now }}),
		audit.NewSQLRepository(db, dialect), ActivityCheckFunc(func(context.Context) (bool, error) { return false, nil }),
		ServiceOptions{Directory: directory, Now: func() time.Time { return now }, NewID: sequenceIDs("backup-a", "audit-a", "backup-safety", "audit-b", "audit-c")})
	require.NoError(t, err)

	created, err := service.Create(context.Background(), "correct horse backup passphrase", KindManual, "request-a", "192.0.2.10")
	require.NoError(t, err)
	require.Equal(t, "backup-a", created.ID)
	require.NotEmpty(t, created.SHA256)
	info, err := os.Stat(filepath.Join(directory, created.Filename))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	data, filename, err := service.Download(context.Background(), created.ID)
	require.NoError(t, err)
	require.Equal(t, created.Filename, filename)
	require.NotContains(t, string(data), "argon-hash")
	require.NotContains(t, string(data), "correct horse backup passphrase")
	validation, err := service.Validate(context.Background(), created.ID, "correct horse backup passphrase")
	require.NoError(t, err)
	require.Equal(t, 1, validation.Manifest["users"])
	_, err = service.Validate(context.Background(), created.ID, "wrong passphrase")
	require.ErrorIs(t, err, ErrInvalidBackup)

	_, err = db.Exec(`UPDATE users SET username = 'Changed', normalized_username = 'changed' WHERE id = 'user-a'`)
	require.NoError(t, err)
	require.NoError(t, service.Restore(context.Background(), created.ID, "correct horse backup passphrase", "request-b", "192.0.2.10"))
	var username string
	require.NoError(t, db.QueryRow(`SELECT username FROM users WHERE id = 'user-a'`).Scan(&username))
	require.Equal(t, "Admin", username)
	listed, err := service.List(context.Background())
	require.NoError(t, err)
	require.Len(t, listed, 2)
	require.Equal(t, KindSafety, listed[0].Kind)
	serialized, err := json.Marshal(listed)
	require.NoError(t, err)
	require.NotContains(t, string(serialized), "passphrase")
	require.NotContains(t, string(serialized), directory)
}

func TestServiceRejectsRestoreWhileWorkIsActive(t *testing.T) {
	checker := ActivityCheckFunc(func(context.Context) (bool, error) { return true, nil })
	service := &Service{activity: checker}
	require.ErrorIs(t, service.Restore(context.Background(), "backup-a", "correct horse backup passphrase", "", ""), ErrActiveWork)
}

func sequenceIDs(values ...string) func() string {
	index := 0
	return func() string { value := values[index]; index++; return value }
}
