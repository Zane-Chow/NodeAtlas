package auth

import (
	"context"
	"crypto/sha256"
	"path/filepath"
	"testing"
	"time"

	"controlpanel/internal/config"
	"controlpanel/internal/database"
	"github.com/stretchr/testify/require"
)

func TestSQLiteRepositoryContract(t *testing.T) {
	db, dialect, err := database.Open(context.Background(), config.DatabaseConfig{
		URL: "sqlite://" + filepath.Join(t.TempDir(), "auth.db"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, database.Migrate(context.Background(), db, dialect))
	runRepositoryContract(t, NewSQLRepository(db, dialect))
}

func runRepositoryContract(t *testing.T, repository Repository) {
	ctx := context.Background()
	now := time.Date(2026, 9, 17, 8, 0, 0, 0, time.UTC)
	user := User{
		ID:                 "11111111-1111-4111-8111-111111111111",
		Username:           "Admin",
		NormalizedUsername: "admin",
		PasswordHash:       "encoded-hash",
		InitializedAt:      now,
	}

	initialized, err := repository.IsInitialized(ctx)
	require.NoError(t, err)
	require.False(t, initialized)
	require.NoError(t, repository.Initialize(ctx, user))
	require.ErrorIs(t, repository.Initialize(ctx, user), ErrAlreadyInitialized)

	stored, err := repository.FindUserByNormalizedUsername(ctx, "admin")
	require.NoError(t, err)
	require.Equal(t, user.ID, stored.ID)
	require.Equal(t, "Admin", stored.Username)

	tokenHash := sha256.Sum256([]byte("session-token"))
	csrfHash := sha256.Sum256([]byte("csrf-token"))
	session := Session{
		TokenHash:         tokenHash,
		CSRFHash:          csrfHash,
		UserID:            user.ID,
		ExpiresAt:         now.Add(12 * time.Hour),
		AbsoluteExpiresAt: now.Add(7 * 24 * time.Hour),
		LastSeenAt:        now,
		CreatedAt:         now,
	}
	require.NoError(t, repository.CreateSession(ctx, session))
	loaded, err := repository.FindSessionByTokenHash(ctx, tokenHash)
	require.NoError(t, err)
	require.Equal(t, session.UserID, loaded.UserID)
	require.Equal(t, session.CSRFHash, loaded.CSRFHash)

	secondHash := sha256.Sum256([]byte("second-session"))
	second := session
	second.TokenHash = secondHash
	require.NoError(t, repository.CreateSession(ctx, second))
	require.NoError(t, repository.RevokeOtherSessions(ctx, user.ID, tokenHash))
	_, err = repository.FindSessionByTokenHash(ctx, secondHash)
	require.ErrorIs(t, err, ErrNotFound)

	touchedAt := now.Add(time.Hour)
	require.NoError(t, repository.TouchSession(ctx, tokenHash, touchedAt, touchedAt.Add(12*time.Hour)))
	loaded, err = repository.FindSessionByTokenHash(ctx, tokenHash)
	require.NoError(t, err)
	require.WithinDuration(t, touchedAt, loaded.LastSeenAt, time.Microsecond)

	require.NoError(t, repository.DeleteSession(ctx, tokenHash))
	_, err = repository.FindSessionByTokenHash(ctx, tokenHash)
	require.ErrorIs(t, err, ErrNotFound)
}
