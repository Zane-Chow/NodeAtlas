package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHTTPLoginSetsSessionAndCSRFCookies(t *testing.T) {
	handler, service := newHTTPHandlerForTest(t, true)
	require.NoError(t, service.Initialize(context.Background(), "admin", "correct horse battery staple"))

	request := jsonRequest(t, http.MethodPost, "/auth/login", map[string]string{
		"username": "admin", "password": "correct horse battery staple",
	})
	request.Header.Set("Origin", "https://panel.example.test")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)
	cookies := response.Result().Cookies()
	require.Len(t, cookies, 2)
	require.Equal(t, "controlpanel_session", cookies[0].Name)
	require.True(t, cookies[0].HttpOnly)
	require.True(t, cookies[0].Secure)
	require.Equal(t, "controlpanel_csrf", cookies[1].Name)
	require.False(t, cookies[1].HttpOnly)
	require.NotContains(t, response.Body.String(), cookies[0].Value)
}

func TestHTTPRejectsWrongOriginAndMissingCSRF(t *testing.T) {
	handler, service := newHTTPHandlerForTest(t, false)
	require.NoError(t, service.Initialize(context.Background(), "admin", "correct horse battery staple"))

	wrongOrigin := jsonRequest(t, http.MethodPost, "/auth/login", map[string]string{
		"username": "admin", "password": "correct horse battery staple",
	})
	wrongOrigin.Header.Set("Origin", "https://evil.example.test")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, wrongOrigin)
	require.Equal(t, http.StatusForbidden, response.Code)

	login := jsonRequest(t, http.MethodPost, "/auth/login", map[string]string{
		"username": "admin", "password": "correct horse battery staple",
	})
	login.Header.Set("Origin", "https://panel.example.test")
	loginResponse := httptest.NewRecorder()
	handler.ServeHTTP(loginResponse, login)
	require.Equal(t, http.StatusOK, loginResponse.Code)

	logout := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	logout.Header.Set("Origin", "https://panel.example.test")
	for _, cookie := range loginResponse.Result().Cookies() {
		logout.AddCookie(cookie)
	}
	logoutResponse := httptest.NewRecorder()
	handler.ServeHTTP(logoutResponse, logout)
	require.Equal(t, http.StatusForbidden, logoutResponse.Code)
}

func TestHTTPSetupClosesAfterInitialization(t *testing.T) {
	handler, _ := newHTTPHandlerForTest(t, false)
	setup := jsonRequest(t, http.MethodPost, "/setup/initialize", map[string]string{
		"username": "admin", "password": "correct horse battery staple",
	})
	setup.Header.Set("Origin", "https://panel.example.test")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, setup)
	require.Equal(t, http.StatusCreated, response.Code)

	again := jsonRequest(t, http.MethodPost, "/setup/initialize", map[string]string{
		"username": "other", "password": "another secure passphrase",
	})
	again.Header.Set("Origin", "https://panel.example.test")
	againResponse := httptest.NewRecorder()
	handler.ServeHTTP(againResponse, again)
	require.Equal(t, http.StatusConflict, againResponse.Code)
	require.Contains(t, againResponse.Body.String(), "already_initialized")
}

func newHTTPHandlerForTest(t *testing.T, secure bool) (http.Handler, *Service) {
	t.Helper()
	repository := openServiceRepository(t)
	service := NewService(repository, NewArgon2idHasher(DefaultArgon2idParams()), ServiceOptions{
		Now:    func() time.Time { return time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC) },
		Random: bytes.NewReader(bytes.Repeat([]byte{4}, 2048)),
	})
	return NewHTTPHandler(service, HTTPOptions{
		PublicOrigin:  "https://panel.example.test",
		SecureCookies: secure,
	}), service
}

func jsonRequest(t *testing.T, method, path string, body any) *http.Request {
	t.Helper()
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	request := httptest.NewRequest(method, path, strings.NewReader(string(encoded)))
	request.Header.Set("Content-Type", "application/json")
	return request
}
