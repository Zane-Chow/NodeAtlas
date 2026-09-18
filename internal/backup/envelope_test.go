package backup

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"controlpanel/internal/database"
	"github.com/stretchr/testify/require"
)

func TestEnvelopeRoundTripAndWrongPassphrase(t *testing.T) {
	archive := Archive{
		FormatVersion: CurrentFormatVersion, ApplicationVersion: "test", CreatedAt: time.Date(2026, 9, 18, 16, 0, 0, 0, time.UTC),
		SourceDialect: database.DialectSQLite, Manifest: map[string]int{"users": 1},
		Tables: map[string]TableData{"users": {Columns: []string{"id"}, Rows: [][]*string{{stringPointer("user-a")}}}},
	}
	sealed, err := Seal(archive, "correct horse backup passphrase", bytes.NewReader(bytes.Repeat([]byte{7}, 128)))
	require.NoError(t, err)
	require.NotContains(t, string(sealed), "user-a")

	opened, err := Open(sealed, "correct horse backup passphrase")
	require.NoError(t, err)
	require.Equal(t, archive.Tables["users"].Rows, opened.Tables["users"].Rows)
	_, err = Open(sealed, "wrong backup passphrase")
	require.ErrorIs(t, err, ErrInvalidBackup)
}

func TestEnvelopeRejectsTamperingAndInvalidManifest(t *testing.T) {
	archive := Archive{FormatVersion: CurrentFormatVersion, CreatedAt: time.Now().UTC(), SourceDialect: database.DialectSQLite, Manifest: map[string]int{}, Tables: map[string]TableData{}}
	sealed, err := Seal(archive, "correct horse backup passphrase", bytes.NewReader(bytes.Repeat([]byte{8}, 128)))
	require.NoError(t, err)
	var envelope encryptedEnvelope
	require.NoError(t, json.Unmarshal(sealed, &envelope))
	envelope.Ciphertext[0] ^= 1
	tampered, err := json.Marshal(envelope)
	require.NoError(t, err)
	_, err = Open(tampered, "correct horse backup passphrase")
	require.ErrorIs(t, err, ErrInvalidBackup)

	archive.Manifest["users"] = 1
	_, err = Seal(archive, "correct horse backup passphrase", bytes.NewReader(bytes.Repeat([]byte{9}, 128)))
	require.ErrorIs(t, err, ErrInvalidManifest)
}

func stringPointer(value string) *string { return &value }
