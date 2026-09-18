package backup

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"

	"golang.org/x/crypto/scrypt"
)

const (
	envelopeMagic = "SCPB"
	scryptN       = 1 << 15
	scryptR       = 8
	scryptP       = 1
)

type encryptedEnvelope struct {
	Magic      string `json:"magic"`
	Version    int    `json:"version"`
	KDF        string `json:"kdf"`
	N          int    `json:"n"`
	R          int    `json:"r"`
	P          int    `json:"p"`
	Salt       []byte `json:"salt"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

func Seal(archive Archive, passphrase string, random io.Reader) ([]byte, error) {
	if err := archive.validateEnvelopeManifest(); err != nil {
		return nil, err
	}
	if len(passphrase) < 12 || len(passphrase) > 1024 {
		return nil, ErrInvalidBackup
	}
	if random == nil {
		random = rand.Reader
	}
	plaintext, err := json.Marshal(archive)
	if err != nil {
		return nil, err
	}
	salt := make([]byte, 16)
	if _, err := io.ReadFull(random, salt); err != nil {
		return nil, err
	}
	key, err := scrypt.Key([]byte(passphrase), salt, scryptN, scryptR, scryptP, 32)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(random, nonce); err != nil {
		return nil, err
	}
	envelope := encryptedEnvelope{Magic: envelopeMagic, Version: CurrentFormatVersion, KDF: "scrypt", N: scryptN, R: scryptR, P: scryptP, Salt: salt, Nonce: nonce}
	envelope.Ciphertext = aead.Seal(nil, nonce, plaintext, []byte("SCPB:1"))
	return json.Marshal(envelope)
}

func Open(data []byte, passphrase string) (Archive, error) {
	if len(passphrase) < 1 || len(data) == 0 || len(data) > 512*1024*1024 {
		return Archive{}, ErrInvalidBackup
	}
	var envelope encryptedEnvelope
	decoder := json.NewDecoder(newByteReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return Archive{}, ErrInvalidBackup
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Archive{}, ErrInvalidBackup
	}
	if envelope.Magic != envelopeMagic || envelope.Version != CurrentFormatVersion || envelope.KDF != "scrypt" || envelope.N != scryptN || envelope.R != scryptR || envelope.P != scryptP || len(envelope.Salt) != 16 || len(envelope.Nonce) != 12 || len(envelope.Ciphertext) < 16 {
		return Archive{}, ErrInvalidBackup
	}
	key, err := scrypt.Key([]byte(passphrase), envelope.Salt, envelope.N, envelope.R, envelope.P, 32)
	if err != nil {
		return Archive{}, ErrInvalidBackup
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return Archive{}, ErrInvalidBackup
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return Archive{}, ErrInvalidBackup
	}
	plaintext, err := aead.Open(nil, envelope.Nonce, envelope.Ciphertext, []byte("SCPB:1"))
	if err != nil {
		return Archive{}, ErrInvalidBackup
	}
	var archive Archive
	if err := json.Unmarshal(plaintext, &archive); err != nil {
		return Archive{}, ErrInvalidBackup
	}
	if err := archive.validateEnvelopeManifest(); err != nil {
		return Archive{}, err
	}
	return archive, nil
}
