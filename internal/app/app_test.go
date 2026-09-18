package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"controlpanel/internal/config"
	"controlpanel/internal/connections"
	"controlpanel/internal/database"
	"controlpanel/internal/inventory"
	"controlpanel/internal/operations"
	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

func TestComposeCreatesAuthenticatedProviderConnection(t *testing.T) {
	temporary := t.TempDir()
	cfg := config.Config{
		Environment: "development",
		HTTP:        config.HTTPConfig{Address: "127.0.0.1:0"},
		Database:    config.DatabaseConfig{URL: "sqlite://" + filepath.Join(temporary, "app.db")},
		Secrets:     config.SecretConfig{CredentialKeys: map[int][]byte{1: bytes.Repeat([]byte{7}, 32)}, ActiveKeyVersion: 1},
		Backup:      config.BackupConfig{Directory: filepath.Join(temporary, "backups")},
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

func TestPowerOperationFlowsThroughAuthenticatedApplication(t *testing.T) {
	temporary := t.TempDir()
	cfg := config.Config{
		Environment: "development", HTTP: config.HTTPConfig{Address: "127.0.0.1:0"},
		Database: config.DatabaseConfig{URL: "sqlite://" + filepath.Join(temporary, "power-app.db")},
		Secrets:  config.SecretConfig{CredentialKeys: map[int][]byte{1: bytes.Repeat([]byte{9}, 32)}, ActiveKeyVersion: 1},
		Backup:   config.BackupConfig{Directory: filepath.Join(temporary, "backups")},
	}
	db, dialect, err := database.Open(context.Background(), cfg.Database)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, database.Migrate(context.Background(), db, dialect))
	handler, worker, err := compose(db, dialect, cfg)
	require.NoError(t, err)

	setup := appRequest(http.MethodPost, "/api/v1/setup/initialize", `{"username":"admin","password":"correct horse battery staple"}`, nil)
	setupResponse := httptest.NewRecorder()
	handler.ServeHTTP(setupResponse, setup)
	require.Equal(t, http.StatusCreated, setupResponse.Code)
	cookies := setupResponse.Result().Cookies()
	create := appRequest(http.MethodPost, "/api/v1/connections", `{
		"name":"Power Lab","provider_type":"mock","enabled":true,
		"settings":{"server_count":1,"seed":1},"credentials":{"token":"power-secret"}
	}`, cookies)
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, create)
	require.Equal(t, http.StatusCreated, createResponse.Code)
	require.NoError(t, worker.RunOnce(context.Background()))

	serversResponse := httptest.NewRecorder()
	handler.ServeHTTP(serversResponse, appRequest(http.MethodGet, "/api/v1/servers", "", cookies))
	var serversPayload struct {
		Servers []inventory.Server `json:"servers"`
	}
	require.NoError(t, json.Unmarshal(serversResponse.Body.Bytes(), &serversPayload))
	require.Len(t, serversPayload.Servers, 1)
	require.Equal(t, inventory.StateStopped, serversPayload.Servers[0].State)

	actionPath := "/api/v1/servers/" + serversPayload.Servers[0].ID + "/actions/start"
	action := appRequest(http.MethodPost, actionPath, "", cookies)
	action.Header.Set("Idempotency-Key", "idem-app")
	actionResponse := httptest.NewRecorder()
	handler.ServeHTTP(actionResponse, action)
	require.Equal(t, http.StatusAccepted, actionResponse.Code)
	var actionPayload struct {
		Operation operations.Operation `json:"operation"`
	}
	require.NoError(t, json.Unmarshal(actionResponse.Body.Bytes(), &actionPayload))
	replayResponse := httptest.NewRecorder()
	replay := appRequest(http.MethodPost, actionPath, "", cookies)
	replay.Header.Set("Idempotency-Key", "idem-app")
	handler.ServeHTTP(replayResponse, replay)
	require.Equal(t, http.StatusOK, replayResponse.Code)
	require.NoError(t, worker.RunOnce(context.Background()))

	operationResponse := httptest.NewRecorder()
	handler.ServeHTTP(operationResponse, appRequest(http.MethodGet, "/api/v1/operations/"+actionPayload.Operation.ID, "", cookies))
	var operationPayload struct {
		Operation operations.Operation `json:"operation"`
	}
	require.NoError(t, json.Unmarshal(operationResponse.Body.Bytes(), &operationPayload))
	require.Equal(t, operations.StatusSucceeded, operationPayload.Operation.Status)
	serverResponse := httptest.NewRecorder()
	handler.ServeHTTP(serverResponse, appRequest(http.MethodGet, "/api/v1/servers/"+serversPayload.Servers[0].ID, "", cookies))
	require.Contains(t, serverResponse.Body.String(), `"state":"running"`)

	consoleOptions := httptest.NewRecorder()
	handler.ServeHTTP(consoleOptions, appRequest(http.MethodGet, "/api/v1/servers/"+serversPayload.Servers[0].ID+"/console-options", "", cookies))
	require.Equal(t, http.StatusOK, consoleOptions.Code)
	require.Contains(t, consoleOptions.Body.String(), `"embedded":{"available":true}`)
	consoleSession := httptest.NewRecorder()
	handler.ServeHTTP(consoleSession, appRequest(http.MethodPost, "/api/v1/servers/"+serversPayload.Servers[0].ID+"/console-sessions", "{}", cookies))
	require.Equal(t, http.StatusCreated, consoleSession.Code)
	require.NotContains(t, consoleSession.Body.String(), "mock+ws")
	var consolePayload struct {
		Session struct {
			Ticket string `json:"ticket"`
		} `json:"session"`
	}
	require.NoError(t, json.Unmarshal(consoleSession.Body.Bytes(), &consolePayload))
	testServer := httptest.NewServer(handler)
	t.Cleanup(testServer.Close)
	cookieValues := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		cookieValues = append(cookieValues, cookie.Name+"="+cookie.Value)
	}
	consoleConnection, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(testServer.URL, "http")+"/ws/console/"+consolePayload.Session.Ticket, &websocket.DialOptions{
		HTTPHeader: http.Header{"Cookie": []string{strings.Join(cookieValues, "; ")}},
	})
	require.NoError(t, err)
	_, banner, err := consoleConnection.Read(context.Background())
	require.NoError(t, err)
	require.Contains(t, string(banner), "Mock Console")
	require.NoError(t, consoleConnection.Close(websocket.StatusNormalClosure, "done"))

	consoleWindow := httptest.NewRecorder()
	handler.ServeHTTP(consoleWindow, appRequest(http.MethodPost, "/api/v1/servers/"+serversPayload.Servers[0].ID+"/console-window", "{}", cookies))
	require.Equal(t, http.StatusOK, consoleWindow.Code)
	require.JSONEq(t, `{"target":{"url":"/api/v1/mock-pages/console"}}`, consoleWindow.Body.String())
	providerPortal := httptest.NewRecorder()
	handler.ServeHTTP(providerPortal, appRequest(http.MethodGet, "/api/v1/servers/"+serversPayload.Servers[0].ID+"/provider-portal", "", cookies))
	require.Equal(t, http.StatusOK, providerPortal.Code)
	require.JSONEq(t, `{"target":{"url":"/api/v1/mock-pages/portal"}}`, providerPortal.Body.String())
	mockPage := httptest.NewRecorder()
	handler.ServeHTTP(mockPage, appRequest(http.MethodGet, "/api/v1/mock-pages/console", "", cookies))
	require.Equal(t, http.StatusOK, mockPage.Code)
	require.Contains(t, mockPage.Header().Get("Content-Security-Policy"), "default-src 'none'")
	require.Contains(t, mockPage.Body.String(), "Mock Console")

	createBackup := httptest.NewRecorder()
	handler.ServeHTTP(createBackup, appRequest(http.MethodPost, "/api/v1/backups", `{"passphrase":"correct horse backup passphrase"}`, cookies))
	require.Equal(t, http.StatusCreated, createBackup.Code)
	require.NotContains(t, createBackup.Body.String(), "passphrase")
	var backupPayload struct {
		Backup struct {
			ID string `json:"id"`
		} `json:"backup"`
	}
	require.NoError(t, json.Unmarshal(createBackup.Body.Bytes(), &backupPayload))
	validateBackup := httptest.NewRecorder()
	handler.ServeHTTP(validateBackup, appRequest(http.MethodPost, "/api/v1/backups/validate", `{"backup_id":"`+backupPayload.Backup.ID+`","passphrase":"correct horse backup passphrase"}`, cookies))
	require.Equal(t, http.StatusOK, validateBackup.Code)

	unauthenticatedEvents := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticatedEvents, httptest.NewRequest(http.MethodGet, "/api/v1/events", nil))
	require.Equal(t, http.StatusUnauthorized, unauthenticatedEvents.Code)
	unauthenticatedConsole := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticatedConsole, httptest.NewRequest(http.MethodGet, "/ws/console/unknown", nil))
	require.Equal(t, http.StatusUnauthorized, unauthenticatedConsole.Code)
}

func appRequest(method, path, body string, cookies []*http.Cookie) *http.Request {
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Host = "panel.example.test"
	for _, cookie := range cookies {
		request.AddCookie(cookie)
		if cookie.Name == "controlpanel_csrf" && method != http.MethodGet {
			request.Header.Set("X-CSRF-Token", cookie.Value)
		}
	}
	if method != http.MethodGet {
		request.Header.Set("Origin", "http://panel.example.test")
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	return request
}
