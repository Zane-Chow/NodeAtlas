package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

var (
	ErrPasswordEmpty    = errors.New("password must not be empty")
	ErrPasswordTooShort = errors.New("password must contain at least 12 bytes")
	ErrPasswordTooLong  = errors.New("password exceeds 1024 bytes")
)

type Argon2idParams struct {
	Memory      uint32
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

type Argon2idHasher struct {
	params Argon2idParams
}

func DefaultArgon2idParams() Argon2idParams {
	return Argon2idParams{
		Memory:      64 * 1024,
		Iterations:  3,
		Parallelism: 2,
		SaltLength:  16,
		KeyLength:   32,
	}
}

func NewArgon2idHasher(params Argon2idParams) *Argon2idHasher {
	return &Argon2idHasher{params: params}
}

func (hasher *Argon2idHasher) Hash(password string) (string, error) {
	if err := validatePassword(password); err != nil {
		return "", err
	}
	salt := make([]byte, hasher.params.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	key := argon2.IDKey(
		[]byte(password),
		salt,
		hasher.params.Iterations,
		hasher.params.Memory,
		hasher.params.Parallelism,
		hasher.params.KeyLength,
	)
	encoding := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		hasher.params.Memory,
		hasher.params.Iterations,
		hasher.params.Parallelism,
		encoding.EncodeToString(salt),
		encoding.EncodeToString(key),
	), nil
}

func (hasher *Argon2idHasher) Verify(encodedHash, password string) (bool, error) {
	if err := validatePassword(password); err != nil {
		return false, err
	}
	if len(encodedHash) > 1024 {
		return false, errors.New("encoded password hash is too large")
	}
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false, errors.New("invalid argon2id hash format")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errors.New("unsupported argon2id version")
	}
	params := Argon2idParams{}
	var parallelism uint32
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &params.Memory, &params.Iterations, &parallelism); err != nil {
		return false, errors.New("invalid argon2id parameters")
	}
	if params.Memory < 8*1024 || params.Memory > 1024*1024 || params.Iterations == 0 || params.Iterations > 20 || parallelism == 0 || parallelism > 16 {
		return false, errors.New("argon2id parameters are outside safe bounds")
	}
	params.Parallelism = uint8(parallelism)
	encoding := base64.RawStdEncoding
	salt, err := encoding.DecodeString(parts[4])
	if err != nil || len(salt) < 8 || len(salt) > 64 {
		return false, errors.New("invalid argon2id salt")
	}
	expected, err := encoding.DecodeString(parts[5])
	if err != nil || len(expected) < 16 || len(expected) > 64 {
		return false, errors.New("invalid argon2id key")
	}
	actual := argon2.IDKey([]byte(password), salt, params.Iterations, params.Memory, params.Parallelism, uint32(len(expected)))
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}

func validatePassword(password string) error {
	if password == "" {
		return ErrPasswordEmpty
	}
	if len([]byte(password)) < 12 {
		return ErrPasswordTooShort
	}
	if len([]byte(password)) > 1024 {
		return ErrPasswordTooLong
	}
	return nil
}
