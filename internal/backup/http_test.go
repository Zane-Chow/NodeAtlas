package backup

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type backupAPIStub struct{}

func (backupAPIStub) List(context.Context) ([]Metadata, error) {
	return []Metadata{{ID: "backup-a", Filename: "backup-a.scpb", Status: StatusReady, CreatedAt: time.Now()}}, nil
}
func (backupAPIStub) Create(context.Context, string, Kind, string, string) (Metadata, error) {
	return Metadata{ID: "backup-a", Status: StatusReady}, nil
}
func (backupAPIStub) Download(context.Context, string) ([]byte, string, error) {
	return []byte("encrypted"), "backup-a.scpb", nil
}
func (backupAPIStub) Validate(context.Context, string, string) (Validation, error) {
	return Validation{FormatVersion: 1, Manifest: map[string]int{"users": 1}}, nil
}
func (backupAPIStub) Restore(context.Context, string, string, string, string) error { return nil }

func TestHTTPHandlerCreatesListsDownloadsValidatesAndRestores(t *testing.T) {
	handler := NewHTTPHandler(backupAPIStub{})
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, httptest.NewRequest(http.MethodPost, "/backups", strings.NewReader(`{"passphrase":"correct horse backup passphrase"}`)))
	require.Equal(t, http.StatusCreated, created.Code)
	require.NotContains(t, created.Body.String(), "passphrase")

	listed := httptest.NewRecorder()
	handler.ServeHTTP(listed, httptest.NewRequest(http.MethodGet, "/backups", nil))
	require.Equal(t, http.StatusOK, listed.Code)
	require.Contains(t, listed.Body.String(), "backup-a")

	download := httptest.NewRecorder()
	handler.ServeHTTP(download, httptest.NewRequest(http.MethodGet, "/backups/backup-a/download", nil))
	require.Equal(t, http.StatusOK, download.Code)
	require.Equal(t, "attachment; filename=\"backup-a.scpb\"", download.Header().Get("Content-Disposition"))

	validated := httptest.NewRecorder()
	handler.ServeHTTP(validated, httptest.NewRequest(http.MethodPost, "/backups/validate", strings.NewReader(`{"backup_id":"backup-a","passphrase":"correct horse backup passphrase"}`)))
	require.Equal(t, http.StatusOK, validated.Code)

	restored := httptest.NewRecorder()
	handler.ServeHTTP(restored, httptest.NewRequest(http.MethodPost, "/backups/backup-a/restore", strings.NewReader(`{"passphrase":"correct horse backup passphrase"}`)))
	require.Equal(t, http.StatusOK, restored.Code)
}
