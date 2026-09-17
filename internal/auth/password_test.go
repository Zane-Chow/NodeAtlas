package auth

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPasswordRoundTrip(t *testing.T) {
	hasher := NewArgon2idHasher(DefaultArgon2idParams())
	encoded, err := hasher.Hash("correct horse battery staple")
	require.NoError(t, err)

	valid, err := hasher.Verify(encoded, "correct horse battery staple")
	require.NoError(t, err)
	require.True(t, valid)
	valid, err = hasher.Verify(encoded, "wrong password")
	require.NoError(t, err)
	require.False(t, valid)
}

func TestPasswordHashUsesIndependentSalts(t *testing.T) {
	hasher := NewArgon2idHasher(DefaultArgon2idParams())
	first, err := hasher.Hash("same password")
	require.NoError(t, err)
	second, err := hasher.Hash("same password")
	require.NoError(t, err)
	require.NotEqual(t, first, second)
}

func TestPasswordRejectsMalformedHashAndOversizedInput(t *testing.T) {
	hasher := NewArgon2idHasher(DefaultArgon2idParams())
	_, err := hasher.Verify("not-a-phc-hash", "password")
	require.Error(t, err)
	_, err = hasher.Hash(strings.Repeat("a", 1025))
	require.ErrorIs(t, err, ErrPasswordTooLong)
}

func TestPasswordRejectsShortInput(t *testing.T) {
	hasher := NewArgon2idHasher(DefaultArgon2idParams())

	_, err := hasher.Hash("short")

	require.ErrorIs(t, err, ErrPasswordTooShort)
}
