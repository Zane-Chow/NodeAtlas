package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadDefaultsToSQLite(t *testing.T) {
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
}

func TestLoadRejectsPartialBootstrapCredentials(t *testing.T) {
	t.Setenv("ADMIN_USERNAME", "admin")
	t.Setenv("ADMIN_PASSWORD", "")

	_, err := Load()

	require.ErrorContains(t, err, "ADMIN_USERNAME and ADMIN_PASSWORD")
}

func TestLoadRequiresHTTPSOriginInProduction(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("PUBLIC_ORIGIN", "http://panel.example.test")

	_, err := Load()

	require.ErrorContains(t, err, "https")
}

func TestLoadAcceptsProductionConfiguration(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("PUBLIC_ORIGIN", "https://panel.example.test")
	t.Setenv("DATABASE_URL", "mysql://panel:secret@mysql:3306/panel")

	cfg, err := Load()

	require.NoError(t, err)
	require.Equal(t, "panel.example.test", cfg.HTTP.PublicOrigin.Host)
	require.Equal(t, "mysql://panel:secret@mysql:3306/panel", cfg.Database.URL)
}

func TestLoadDoesNotExposeMalformedDatabaseSecret(t *testing.T) {
	t.Setenv("DATABASE_URL", "mysql://admin:super-secret%zz@mysql:3306/panel")

	_, err := Load()

	require.Error(t, err)
	require.NotContains(t, err.Error(), "super-secret")
}
