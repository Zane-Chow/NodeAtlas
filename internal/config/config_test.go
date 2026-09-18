package config

import (
	"bytes"
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"
)

func setValidCredentialKeys(t *testing.T) {
	t.Helper()
	t.Setenv("CREDENTIAL_KEYS", "1:"+base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)))
	t.Setenv("CREDENTIAL_ACTIVE_KEY_VERSION", "1")
	t.Setenv("CONSOLE_ALLOWED_PRIVATE_CIDRS", "")
}

func TestLoadDefaultsToSQLite(t *testing.T) {
	setValidCredentialKeys(t)
	t.Setenv("DATABASE_URL", "")
	t.Setenv("APP_ENV", "")
	t.Setenv("PUBLIC_ORIGIN", "")
	t.Setenv("ADMIN_USERNAME", "")
	t.Setenv("ADMIN_PASSWORD", "")

	cfg, err := Load()

	require.NoError(t, err)
	require.Equal(t, "sqlite://data/controlpanel.db", cfg.Database.URL)
	require.Equal(t, "127.0.0.1:8080", cfg.HTTP.Address)
	require.Equal(t, "development", cfg.Environment)
	require.Equal(t, "data/backups", cfg.Backup.Directory)
}

func TestLoadAcceptsBackupDirectory(t *testing.T) {
	setValidCredentialKeys(t)
	t.Setenv("BACKUP_DIRECTORY", "/var/lib/controlpanel/backups")

	cfg, err := Load()

	require.NoError(t, err)
	require.Equal(t, "/var/lib/controlpanel/backups", cfg.Backup.Directory)
}

func TestLoadAcceptsConsoleAllowedPrivateCIDRs(t *testing.T) {
	setValidCredentialKeys(t)
	t.Setenv("CONSOLE_ALLOWED_PRIVATE_CIDRS", "10.20.0.0/16, fd00::/8")

	cfg, err := Load()

	require.NoError(t, err)
	require.Equal(t, []string{"10.20.0.0/16", "fd00::/8"}, cfg.Console.AllowedPrivateCIDRs)
}

func TestLoadRejectsMalformedConsoleAllowedPrivateCIDR(t *testing.T) {
	setValidCredentialKeys(t)
	t.Setenv("CONSOLE_ALLOWED_PRIVATE_CIDRS", "10.20.0.0/not-a-prefix")

	_, err := Load()

	require.ErrorContains(t, err, "CONSOLE_ALLOWED_PRIVATE_CIDRS")
}

func TestLoadRejectsPublicConsoleAllowedCIDR(t *testing.T) {
	setValidCredentialKeys(t)
	t.Setenv("CONSOLE_ALLOWED_PRIVATE_CIDRS", "203.0.113.0/24")

	_, err := Load()

	require.ErrorContains(t, err, "private network")
}

func TestLoadRejectsPartialBootstrapCredentials(t *testing.T) {
	setValidCredentialKeys(t)
	t.Setenv("ADMIN_USERNAME", "admin")
	t.Setenv("ADMIN_PASSWORD", "")

	_, err := Load()

	require.ErrorContains(t, err, "ADMIN_USERNAME and ADMIN_PASSWORD")
}

func TestLoadRequiresHTTPSOriginInProduction(t *testing.T) {
	setValidCredentialKeys(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("PUBLIC_ORIGIN", "http://panel.example.test")

	_, err := Load()

	require.ErrorContains(t, err, "https")
}

func TestLoadAcceptsProductionConfiguration(t *testing.T) {
	setValidCredentialKeys(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("PUBLIC_ORIGIN", "https://panel.example.test")
	t.Setenv("DATABASE_URL", "mysql://panel:secret@mysql:3306/panel")

	cfg, err := Load()

	require.NoError(t, err)
	require.Equal(t, "panel.example.test", cfg.HTTP.PublicOrigin.Host)
	require.Equal(t, "mysql://panel:secret@mysql:3306/panel", cfg.Database.URL)
}

func TestLoadDoesNotExposeMalformedDatabaseSecret(t *testing.T) {
	setValidCredentialKeys(t)
	t.Setenv("DATABASE_URL", "mysql://admin:super-secret%zz@mysql:3306/panel")

	_, err := Load()

	require.Error(t, err)
	require.NotContains(t, err.Error(), "super-secret")
}

func TestLoadRequiresCredentialKeys(t *testing.T) {
	t.Setenv("CREDENTIAL_KEYS", "")
	t.Setenv("CREDENTIAL_ACTIVE_KEY_VERSION", "")

	_, err := Load()

	require.ErrorContains(t, err, "CREDENTIAL_KEYS")
}

func TestLoadParsesCredentialKeyRing(t *testing.T) {
	first := bytes.Repeat([]byte{3}, 32)
	second := bytes.Repeat([]byte{4}, 32)
	t.Setenv("CREDENTIAL_KEYS", "1:"+base64.StdEncoding.EncodeToString(first)+",2:"+base64.StdEncoding.EncodeToString(second))
	t.Setenv("CREDENTIAL_ACTIVE_KEY_VERSION", "2")

	cfg, err := Load()

	require.NoError(t, err)
	require.Equal(t, 2, cfg.Secrets.ActiveKeyVersion)
	require.Equal(t, first, cfg.Secrets.CredentialKeys[1])
	require.Equal(t, second, cfg.Secrets.CredentialKeys[2])
}

func TestLoadRejectsMissingActiveCredentialKey(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32))
	t.Setenv("CREDENTIAL_KEYS", "1:"+key)
	t.Setenv("CREDENTIAL_ACTIVE_KEY_VERSION", "2")

	_, err := Load()

	require.ErrorContains(t, err, "active credential key version")
}
