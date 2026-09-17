package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
)

var (
	ErrDecryptCredentials = errors.New("unable to decrypt credentials")
	ErrUnknownKeyVersion  = errors.New("unknown credential key version")
)

type Envelope struct {
	Ciphertext []byte
	Nonce      []byte
	KeyVersion int
}

type CredentialCipher struct {
	keys          map[int][]byte
	activeVersion int
}

func NewCredentialCipher(keys map[int][]byte, activeVersion int) (*CredentialCipher, error) {
	if len(keys) == 0 {
		return nil, errors.New("at least one credential key is required")
	}
	copied := make(map[int][]byte, len(keys))
	for version, key := range keys {
		if version < 1 {
			return nil, errors.New("credential key versions must be positive")
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("credential key version %d must be 32 bytes", version)
		}
		copied[version] = append([]byte(nil), key...)
	}
	if _, ok := copied[activeVersion]; !ok {
		return nil, errors.New("active credential key version is not configured")
	}
	return &CredentialCipher{keys: copied, activeVersion: activeVersion}, nil
}

func (credentialCipher *CredentialCipher) Encrypt(connectionID, providerType string, plaintext []byte) (Envelope, error) {
	if connectionID == "" || providerType == "" {
		return Envelope{}, errors.New("connection ID and provider type are required")
	}
	aead, err := newGCM(credentialCipher.keys[credentialCipher.activeVersion])
	if err != nil {
		return Envelope{}, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return Envelope{}, errors.New("generate credential nonce")
	}
	ciphertext := aead.Seal(nil, nonce, plaintext, additionalData(connectionID, providerType, credentialCipher.activeVersion))
	return Envelope{Ciphertext: ciphertext, Nonce: nonce, KeyVersion: credentialCipher.activeVersion}, nil
}

func (credentialCipher *CredentialCipher) Decrypt(connectionID, providerType string, envelope Envelope) ([]byte, error) {
	key, ok := credentialCipher.keys[envelope.KeyVersion]
	if !ok {
		return nil, fmt.Errorf("%w: %d", ErrUnknownKeyVersion, envelope.KeyVersion)
	}
	aead, err := newGCM(key)
	if err != nil || len(envelope.Nonce) != aead.NonceSize() {
		return nil, ErrDecryptCredentials
	}
	plaintext, err := aead.Open(nil, envelope.Nonce, envelope.Ciphertext, additionalData(connectionID, providerType, envelope.KeyVersion))
	if err != nil {
		return nil, ErrDecryptCredentials
	}
	return plaintext, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func additionalData(connectionID, providerType string, version int) []byte {
	return []byte(fmt.Sprintf("controlpanel:credentials:v%d\x00%s\x00%s", version, providerType, connectionID))
}
