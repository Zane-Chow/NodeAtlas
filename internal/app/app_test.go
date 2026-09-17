package app

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"controlpanel/internal/config"
	"controlpanel/internal/connections"
	"controlpanel/internal/database"
	"github.com/stretchr/testify/require"
)

func TestComposeCreatesAuthenticatedProviderConnection(t *testing.T) {
	cfg := config.Config{
		Environment: "development",
		HTTP:        config.HTTPConfig{Address: "127.0.0.1:0"},
		Database:    config.DatabaseConfig{URL: "sqlite://" + filepath.Join(t.TempDir(), "app.db")},
		Secrets:     config.SecretConfig{CredentialKeys: map[int][]byte{1: bytes.Repeat([]byte{7}, 32)}, ActiveKeyVersion: 1},
	}
	db, dialect, err := database.Open(context.Background(), cfg.Database)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, database.Migrate(context.Background(), db, dialect))
	handler, _, err := compose(db, dialect, cfg)
	require.NoError(t, err)

	setup := httptest.NewRequest(http.MethodPost, "/api/v1/setup/initialize", bytes.NewBufferString(`{
		"username":"admin","password":"correct horse battery staple"
	}`))
	setup.Header.Set("Content-Type", "application/json")
	setup.Header.Set("Origin", "http://panel.example.test")
	setup.Host = "panel.example.test"
	setupResponse := httptest.NewRecorder()
	handler.ServeHTTP(setupResponse, setup)
	require.Equal(t, http.StatusCreated, setupResponse.Code)

	create := httptest.NewRequest(http.MethodPost, "/api/v1/connections", bytes.NewBufferString(`{
		"name":"Lab A","provider_type":"mock","enabled":true,
		"settings":{"server_count":2},"credentials":{"token":"write-only"}
	}`))
	create.Header.Set("Content-Type", "application/json")
	create.Header.Set("Origin", "http://panel.example.test")
	create.Host = "panel.example.test"
	for _, cookie := range setupResponse.Result().Cookies() {
		create.AddCookie(cookie)
		if cookie.Name == "controlpanel_csrf" {
			create.Header.Set("X-CSRF-Token", cookie.Value)
		}
	}
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, create)
	require.Equal(t, http.StatusCreated, createResponse.Code)
	require.NotContains(t, createResponse.Body.String(), "write-only")
	servers := httptest.NewRequest(http.MethodGet, "/api/v1/servers", nil)
	for _, cookie := range setupResponse.Result().Cookies() {
		servers.AddCookie(cookie)
	}
	serversResponse := httptest.NewRecorder()
	handler.ServeHTTP(serversResponse, servers)
	require.Equal(t, http.StatusOK, serversResponse.Code)
	require.Contains(t, serversResponse.Body.String(), `"total":0`)

	stored, err := connections.NewSQLRepository(db, dialect).List(context.Background())
	require.NoError(t, err)
	require.Len(t, stored, 1)
	require.Equal(t, "Lab A", stored[0].Name)
}
