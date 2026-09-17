package console

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net"
	"net/url"
	"testing"
	"time"

	"controlpanel/internal/audit"
	"controlpanel/internal/connections"
	"controlpanel/internal/inventory"
	"controlpanel/internal/providers"
	"controlpanel/internal/secrets"
	"github.com/stretchr/testify/require"
)

func TestServiceExposesCapabilityOrderedOptions(t *testing.T) {
	fixture := newServiceFixture(t)
	options, err := fixture.service.Options(context.Background(), "server-a")
	require.NoError(t, err)
	require.True(t, options.Embedded.Available)
	require.True(t, options.Window.Available)
	require.True(t, options.Portal.Available)
	require.Equal(t, []string{"embedded", "window", "portal"}, options.Order)
}

func TestServiceCreatesHashedOneUseEmbeddedTicketWithoutExposingTarget(t *testing.T) {
	fixture := newServiceFixture(t)
	created, err := fixture.service.CreateEmbedded(context.Background(), "server-a", "request-a", "192.0.2.10")
	require.NoError(t, err)
	require.Equal(t, "fixed-console-ticket", created.Ticket)
	require.Equal(t, fixture.now.Add(time.Minute), created.ExpiresAt)
	require.NotContains(t, string(mustJSON(t, created)), "provider-temporary-secret")

	expectedHash := sha256.Sum256([]byte("fixed-console-ticket"))
	require.Equal(t, expectedHash[:], fixture.repository.created.TicketHash)
	target, ok := fixture.targets.Take(fixture.repository.created.ID)
	require.True(t, ok)
	require.Contains(t, target.String(), "provider-temporary-secret")
	require.Len(t, fixture.audit.entries, 1)
	require.NotContains(t, string(fixture.audit.entries[0].Metadata), "provider-temporary-secret")
}

func TestServiceReturnsOnlyValidatedWindowAndPortalURLs(t *testing.T) {
	fixture := newServiceFixture(t)
	window, err := fixture.service.OpenWindow(context.Background(), "server-a", "request-a", "192.0.2.10")
	require.NoError(t, err)
	require.Equal(t, "https://console.example.test/window", window.URL)
	portal, err := fixture.service.ProviderPortal(context.Background(), "server-a", "request-b", "192.0.2.10")
	require.NoError(t, err)
	require.Equal(t, "https://portal.example.test/servers/server-a", portal.URL)
}

type serviceFixture struct {
	service    *Service
	repository *memorySessionRepository
	targets    *MemoryTargetStore
	audit      *memoryAudit
	now        time.Time
}

func newServiceFixture(t *testing.T) serviceFixture {
	t.Helper()
	now := time.Date(2026, 9, 18, 14, 0, 0, 0, time.UTC)
	key := make([]byte, 32)
	cipher, err := secrets.NewCredentialCipher(map[int][]byte{1: key}, 1)
	require.NoError(t, err)
	envelope, err := cipher.Encrypt("connection-a", "fake", []byte(`{"token":"credential-secret"}`))
	require.NoError(t, err)
	connectionRepository := &memoryConnections{connection: connections.Connection{ID: "connection-a", ProviderType: "fake", Enabled: true}, credentials: connections.CredentialRecord{
		Ciphertext: envelope.Ciphertext, Nonce: envelope.Nonce, KeyVersion: envelope.KeyVersion,
	}}
	registry := providers.NewRegistry()
	require.NoError(t, registry.Register("fake", fakeConsoleFactory{}))
	repository := &memorySessionRepository{}
	targets := NewMemoryTargetStore()
	auditLog := &memoryAudit{}
	policy := NewTargetPolicy(staticResolver{
		"console.example.test": {net.ParseIP("203.0.113.10")},
		"portal.example.test":  {net.ParseIP("203.0.113.11")},
	}, TargetPolicyOptions{})
	service := NewService(repository, memoryInventory{server: inventory.Server{
		ID: "server-a", ConnectionID: "connection-a", ExternalID: "remote-a", Scope: "zone-a", State: inventory.StateRunning,
		Capabilities: json.RawMessage(`{"can_embed_console":{"available":true},"can_open_console_window":{"available":true},"has_provider_portal":{"available":true}}`),
	}}, connectionRepository, auditLog, cipher, registry, policy, targets, ServiceOptions{
		Now: func() time.Time { return now }, NewID: func() string { return "session-a" },
		NewTicket: func() string { return "fixed-console-ticket" }, TicketTTL: time.Minute,
	})
	return serviceFixture{service: service, repository: repository, targets: targets, audit: auditLog, now: now}
}

type memoryInventory struct{ server inventory.Server }

func (repository memoryInventory) FindByID(_ context.Context, id string) (inventory.Server, error) {
	if id != repository.server.ID {
		return inventory.Server{}, inventory.ErrNotFound
	}
	return repository.server, nil
}

type memoryConnections struct {
	connection  connections.Connection
	credentials connections.CredentialRecord
}

func (repository *memoryConnections) FindByID(_ context.Context, id string) (connections.Connection, connections.CredentialRecord, error) {
	return repository.connection, repository.credentials, nil
}

type memorySessionRepository struct{ created Session }

func (repository *memorySessionRepository) Create(_ context.Context, session Session) error {
	repository.created = session
	return nil
}
func (repository *memorySessionRepository) ConsumeTicket(context.Context, []byte, time.Time) (Session, error) {
	return Session{}, ErrNotFound
}
func (repository *memorySessionRepository) FindByID(context.Context, string) (Session, error) {
	return repository.created, nil
}
func (repository *memorySessionRepository) Close(context.Context, string, Result, time.Time) error {
	return nil
}

type memoryAudit struct{ entries []audit.Entry }

func (log *memoryAudit) Append(_ context.Context, entry audit.Entry) error {
	log.entries = append(log.entries, entry)
	return nil
}

type fakeConsoleFactory struct{}

func (fakeConsoleFactory) Create(providers.ConnectionConfig) (providers.Provider, error) {
	return fakeConsoleProvider{}, nil
}

type fakeConsoleProvider struct{}

func (fakeConsoleProvider) ValidateConnection(context.Context) (providers.ConnectionInfo, error) {
	return providers.ConnectionInfo{}, nil
}
func (fakeConsoleProvider) ListServers(context.Context, *providers.Cursor) (providers.ServerPage, error) {
	return providers.ServerPage{}, nil
}
func (fakeConsoleProvider) GetServer(context.Context, providers.ServerRef) (providers.RemoteServer, error) {
	return providers.RemoteServer{}, nil
}
func (fakeConsoleProvider) StartServer(context.Context, providers.ServerRef) (providers.ActionReceipt, error) {
	return providers.ActionReceipt{}, nil
}
func (fakeConsoleProvider) StopServer(context.Context, providers.ServerRef) (providers.ActionReceipt, error) {
	return providers.ActionReceipt{}, nil
}
func (fakeConsoleProvider) RebootServer(context.Context, providers.ServerRef) (providers.ActionReceipt, error) {
	return providers.ActionReceipt{}, nil
}
func (fakeConsoleProvider) OpenConsole(_ context.Context, _ providers.ServerRef, mode providers.ConsoleMode) (providers.ConsoleTarget, error) {
	if mode == providers.ConsoleEmbedded {
		return providers.ConsoleTarget{Mode: mode, URL: mustProviderURL("wss://console.example.test/embedded?token=provider-temporary-secret")}, nil
	}
	return providers.ConsoleTarget{Mode: mode, URL: mustProviderURL("https://console.example.test/window")}, nil
}
func (fakeConsoleProvider) ProviderPortalURL(context.Context, providers.ServerRef) (*url.URL, error) {
	return mustProviderURL("https://portal.example.test/servers/server-a"), nil
}

func mustProviderURL(raw string) *url.URL { parsed, _ := url.Parse(raw); return parsed }
func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return encoded
}
