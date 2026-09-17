package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"path/filepath"
	"testing"
	"time"

	"controlpanel/internal/config"
	"controlpanel/internal/database"
	"github.com/stretchr/testify/require"
)

func TestServiceInitializesOnceAndAuthenticatesWithoutStoringRawToken(t *testing.T) {
	repository := openServiceRepository(t)
	now := time.Date(2026, 9, 17, 9, 0, 0, 0, time.UTC)
	service := NewService(repository, NewArgon2idHasher(DefaultArgon2idParams()), ServiceOptions{
		Now:    func() time.Time { return now },
		Random: bytes.NewReader(bytes.Repeat([]byte{7}, 256)),
	})

	initialized, err := service.SetupStatus(context.Background())
	require.NoError(t, err)
	require.False(t, initialized)
	require.NoError(t, service.Initialize(context.Background(), " Admin ", "correct horse battery staple"))
	require.ErrorIs(t, service.Initialize(context.Background(), "other", "another secure passphrase"), ErrAlreadyInitialized)

	grant, err := service.Login(context.Background(), "ADMIN", "correct horse battery staple")
	require.NoError(t, err)
	require.NotEmpty(t, grant.SessionToken)
	require.NotEmpty(t, grant.CSRFToken)
	require.Equal(t, "Admin", grant.User.Username)

	tokenHash := sha256.Sum256([]byte(grant.SessionToken))
	stored, err := repository.FindSessionByTokenHash(context.Background(), tokenHash)
	require.NoError(t, err)
	require.Equal(t, tokenHash, stored.TokenHash)
	require.NotEqual(t, grant.SessionToken, string(stored.TokenHash[:]))
}

func TestServiceUsesGenericLoginErrorAndRejectsExpiredSession(t *testing.T) {
	repository := openServiceRepository(t)
	now := time.Date(2026, 9, 17, 9, 0, 0, 0, time.UTC)
	service := NewService(repository, NewArgon2idHasher(DefaultArgon2idParams()), ServiceOptions{
		Now:     func() time.Time { return now },
		Random:  bytes.NewReader(bytes.Repeat([]byte{9}, 256)),
		IdleTTL: time.Hour,
	})
	require.NoError(t, service.Initialize(context.Background(), "admin", "correct horse battery staple"))

	_, unknownErr := service.Login(context.Background(), "missing", "some password")
	_, wrongErr := service.Login(context.Background(), "admin", "wrong password")
	require.ErrorIs(t, unknownErr, ErrInvalidCredentials)
	require.ErrorIs(t, wrongErr, ErrInvalidCredentials)

	grant, err := service.Login(context.Background(), "admin", "correct horse battery staple")
	require.NoError(t, err)
	now = now.Add(2 * time.Hour)
	_, err = service.Authenticate(context.Background(), grant.SessionToken)
	require.ErrorIs(t, err, ErrInvalidSession)
}

func openServiceRepository(t *testing.T) *SQLRepository {
	t.Helper()
	db, dialect, err := database.Open(context.Background(), config.DatabaseConfig{
		URL: "sqlite://" + filepath.Join(t.TempDir(), "service.db"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, database.Migrate(context.Background(), db, dialect))
	return NewSQLRepository(db, dialect)
}
