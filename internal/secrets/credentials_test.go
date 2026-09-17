package secrets

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCredentialCipherRoundTrip(t *testing.T) {
	cipher, err := NewCredentialCipher(map[int][]byte{1: bytes.Repeat([]byte{7}, 32)}, 1)
	require.NoError(t, err)

	envelope, err := cipher.Encrypt("connection-a", "mock", []byte(`{"token":"secret"}`))
	require.NoError(t, err)

	plaintext, err := cipher.Decrypt("connection-a", "mock", envelope)
	require.NoError(t, err)
	require.JSONEq(t, `{"token":"secret"}`, string(plaintext))
	require.Equal(t, 1, envelope.KeyVersion)
	require.Len(t, envelope.Nonce, 12)
}

func TestCredentialCipherUsesUniqueNonces(t *testing.T) {
	cipher, err := NewCredentialCipher(map[int][]byte{1: bytes.Repeat([]byte{7}, 32)}, 1)
	require.NoError(t, err)

	first, err := cipher.Encrypt("connection-a", "mock", []byte(`{"token":"secret"}`))
	require.NoError(t, err)
	second, err := cipher.Encrypt("connection-a", "mock", []byte(`{"token":"secret"}`))
	require.NoError(t, err)

	require.NotEqual(t, first.Nonce, second.Nonce)
	require.NotEqual(t, first.Ciphertext, second.Ciphertext)
}

func TestCredentialCipherBindsConnectionAndProviderAsAdditionalData(t *testing.T) {
	cipher, err := NewCredentialCipher(map[int][]byte{1: bytes.Repeat([]byte{7}, 32)}, 1)
	require.NoError(t, err)
	envelope, err := cipher.Encrypt("connection-a", "mock", []byte(`{"token":"secret"}`))
	require.NoError(t, err)

	_, wrongConnectionErr := cipher.Decrypt("connection-b", "mock", envelope)
	_, wrongProviderErr := cipher.Decrypt("connection-a", "aws", envelope)

	require.ErrorIs(t, wrongConnectionErr, ErrDecryptCredentials)
	require.ErrorIs(t, wrongProviderErr, ErrDecryptCredentials)
}

func TestCredentialCipherRejectsUnknownKeyVersion(t *testing.T) {
	cipher, err := NewCredentialCipher(map[int][]byte{1: bytes.Repeat([]byte{7}, 32)}, 1)
	require.NoError(t, err)

	_, err = cipher.Decrypt("connection-a", "mock", Envelope{
		Ciphertext: []byte("ciphertext"),
		Nonce:      bytes.Repeat([]byte{1}, 12),
		KeyVersion: 2,
	})

	require.ErrorIs(t, err, ErrUnknownKeyVersion)
}

func TestNewCredentialCipherRequiresAES256Keys(t *testing.T) {
	_, err := NewCredentialCipher(map[int][]byte{1: bytes.Repeat([]byte{7}, 16)}, 1)

	require.ErrorContains(t, err, "32 bytes")
}
