package console

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"

	"controlpanel/internal/audit"
	"controlpanel/internal/connections"
	"controlpanel/internal/inventory"
	"controlpanel/internal/providers"
	"controlpanel/internal/secrets"
	"github.com/google/uuid"
)

var (
	ErrCapabilityUnavailable = errors.New("console capability is unavailable")
	ErrTargetUnavailable     = errors.New("console target is unavailable")
)

type InventoryRepository interface {
	FindByID(context.Context, string) (inventory.Server, error)
}
type ConnectionRepository interface {
	FindByID(context.Context, string) (connections.Connection, connections.CredentialRecord, error)
}
type AuditAppender interface {
	Append(context.Context, audit.Entry) error
}

type Option struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}
type ConsoleOptions struct {
	Embedded Option   `json:"embedded"`
	Window   Option   `json:"window"`
	Portal   Option   `json:"portal"`
	Order    []string `json:"order"`
}
type SessionTicket struct {
	SessionID   string              `json:"session_id"`
	Ticket      string              `json:"ticket"`
	ExpiresAt   time.Time           `json:"expires_at"`
	Protocol    string              `json:"protocol"`
	Credentials *SessionCredentials `json:"credentials,omitempty"`
}

type SessionCredentials struct {
	Password string `json:"password"`
}
type ExternalTarget struct {
	URL string `json:"url"`
}

type ServiceOptions struct {
	Now        func() time.Time
	NewID      func() string
	NewTicket  func() string
	NewAuditID func() string
	TicketTTL  time.Duration
}

type Service struct {
	sessions    Repository
	inventory   InventoryRepository
	connections ConnectionRepository
	audit       AuditAppender
	cipher      *secrets.CredentialCipher
	registry    *providers.Registry
	policy      *TargetPolicy
	targets     *MemoryTargetStore
	options     ServiceOptions
}

func NewService(sessions Repository, inventoryRepository InventoryRepository, connectionRepository ConnectionRepository, auditLog AuditAppender, cipher *secrets.CredentialCipher, registry *providers.Registry, policy *TargetPolicy, targets *MemoryTargetStore, options ServiceOptions) *Service {
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	if options.NewID == nil {
		options.NewID = uuid.NewString
	}
	if options.NewTicket == nil {
		options.NewTicket = randomTicket
	}
	if options.NewAuditID == nil {
		options.NewAuditID = uuid.NewString
	}
	if options.TicketTTL <= 0 {
		options.TicketTTL = time.Minute
	}
	if targets == nil {
		targets = NewMemoryTargetStore()
	}
	return &Service{sessions: sessions, inventory: inventoryRepository, connections: connectionRepository, audit: auditLog, cipher: cipher, registry: registry, policy: policy, targets: targets, options: options}
}

func (service *Service) Options(ctx context.Context, serverID string) (ConsoleOptions, error) {
	server, capabilities, err := service.serverCapabilities(ctx, serverID)
	if err != nil {
		return ConsoleOptions{}, err
	}
	_ = server
	return ConsoleOptions{
		Embedded: optionFromCapability(capabilities.CanEmbedConsole),
		Window:   optionFromCapability(capabilities.CanOpenConsoleWindow),
		Portal:   optionFromCapability(capabilities.HasProviderPortal),
		Order:    []string{"embedded", "window", "portal"},
	}, nil
}

func (service *Service) CreateEmbedded(ctx context.Context, serverID, requestID, sourceIP string) (SessionTicket, error) {
	server, capabilities, err := service.serverCapabilities(ctx, serverID)
	if err != nil {
		return SessionTicket{}, err
	}
	if !capabilities.CanEmbedConsole.Available {
		return SessionTicket{}, ErrCapabilityUnavailable
	}
	provider, _, err := service.provider(ctx, server.ConnectionID)
	if err != nil {
		return SessionTicket{}, err
	}
	target, err := provider.OpenConsole(ctx, providers.ServerRef{ExternalID: server.ExternalID, Scope: server.Scope}, providers.ConsoleEmbedded)
	if err != nil || target.URL == nil {
		if err != nil {
			return SessionTicket{}, err
		}
		return SessionTicket{}, ErrTargetUnavailable
	}
	if err := service.policy.ValidateEmbedded(ctx, target.URL); err != nil {
		return SessionTicket{}, err
	}
	now := service.options.Now().UTC()
	ticket := service.options.NewTicket()
	hash := sha256.Sum256([]byte(ticket))
	session := Session{ID: service.options.NewID(), ServerID: server.ID, Mode: ModeEmbedded, TicketHash: hash[:], ExpiresAt: now.Add(service.options.TicketTTL), Result: ResultPending, CreatedAt: now}
	if err := service.sessions.Create(ctx, session); err != nil {
		return SessionTicket{}, err
	}
	service.targets.Put(session.ID, target.URL)
	if err := service.appendAudit(ctx, session.ID, "console_session_created", requestID, sourceIP, map[string]string{"mode": "embedded"}); err != nil {
		service.targets.Delete(session.ID)
		_ = service.sessions.Close(ctx, session.ID, ResultFailed, now)
		return SessionTicket{}, err
	}
	protocol := target.Protocol
	if protocol == "" {
		protocol = "terminal"
	}
	created := SessionTicket{SessionID: session.ID, Ticket: ticket, ExpiresAt: session.ExpiresAt, Protocol: protocol}
	if protocol == "rfb" && target.Password != "" {
		created.Credentials = &SessionCredentials{Password: target.Password}
	}
	return created, nil
}

func (service *Service) OpenWindow(ctx context.Context, serverID, requestID, sourceIP string) (ExternalTarget, error) {
	return service.externalTarget(ctx, serverID, providers.ConsoleWindow, requestID, sourceIP)
}

func (service *Service) ProviderPortal(ctx context.Context, serverID, requestID, sourceIP string) (ExternalTarget, error) {
	server, capabilities, err := service.serverCapabilities(ctx, serverID)
	if err != nil {
		return ExternalTarget{}, err
	}
	if !capabilities.HasProviderPortal.Available {
		return ExternalTarget{}, ErrCapabilityUnavailable
	}
	provider, providerType, err := service.provider(ctx, server.ConnectionID)
	if err != nil {
		return ExternalTarget{}, err
	}
	target, err := provider.ProviderPortalURL(ctx, providers.ServerRef{ExternalID: server.ExternalID, Scope: server.Scope})
	if err != nil || target == nil {
		if err != nil {
			return ExternalTarget{}, err
		}
		return ExternalTarget{}, ErrTargetUnavailable
	}
	if mockPath, ok := trustedMockPage(providerType, target); ok {
		if err := service.appendAudit(ctx, server.ID, "provider_portal_opened", requestID, sourceIP, map[string]string{"mode": "portal"}); err != nil {
			return ExternalTarget{}, err
		}
		return ExternalTarget{URL: mockPath}, nil
	}
	if err := service.policy.ValidateExternal(ctx, target); err != nil {
		return ExternalTarget{}, err
	}
	if err := service.appendAudit(ctx, server.ID, "provider_portal_opened", requestID, sourceIP, map[string]string{"mode": "portal"}); err != nil {
		return ExternalTarget{}, err
	}
	return ExternalTarget{URL: target.String()}, nil
}

func (service *Service) externalTarget(ctx context.Context, serverID string, mode providers.ConsoleMode, requestID, sourceIP string) (ExternalTarget, error) {
	server, capabilities, err := service.serverCapabilities(ctx, serverID)
	if err != nil {
		return ExternalTarget{}, err
	}
	if mode != providers.ConsoleWindow || !capabilities.CanOpenConsoleWindow.Available {
		return ExternalTarget{}, ErrCapabilityUnavailable
	}
	provider, providerType, err := service.provider(ctx, server.ConnectionID)
	if err != nil {
		return ExternalTarget{}, err
	}
	target, err := provider.OpenConsole(ctx, providers.ServerRef{ExternalID: server.ExternalID, Scope: server.Scope}, mode)
	if err != nil || target.URL == nil {
		if err != nil {
			return ExternalTarget{}, err
		}
		return ExternalTarget{}, ErrTargetUnavailable
	}
	if mockPath, ok := trustedMockPage(providerType, target.URL); ok {
		if err := service.appendAudit(ctx, server.ID, "console_window_opened", requestID, sourceIP, map[string]string{"mode": "window"}); err != nil {
			return ExternalTarget{}, err
		}
		return ExternalTarget{URL: mockPath}, nil
	}
	if err := service.policy.ValidateExternal(ctx, target.URL); err != nil {
		return ExternalTarget{}, err
	}
	if err := service.appendAudit(ctx, server.ID, "console_window_opened", requestID, sourceIP, map[string]string{"mode": "window"}); err != nil {
		return ExternalTarget{}, err
	}
	return ExternalTarget{URL: target.URL.String()}, nil
}

func (service *Service) serverCapabilities(ctx context.Context, serverID string) (inventory.Server, providers.Capabilities, error) {
	server, err := service.inventory.FindByID(ctx, serverID)
	if err != nil {
		return inventory.Server{}, providers.Capabilities{}, err
	}
	var capabilities providers.Capabilities
	if err := json.Unmarshal(server.Capabilities, &capabilities); err != nil {
		return inventory.Server{}, providers.Capabilities{}, fmt.Errorf("decode console capabilities: %w", err)
	}
	return server, capabilities, nil
}

func (service *Service) provider(ctx context.Context, connectionID string) (providers.Provider, string, error) {
	connection, stored, err := service.connections.FindByID(ctx, connectionID)
	if err != nil {
		return nil, "", err
	}
	plaintext, err := service.cipher.Decrypt(connection.ID, connection.ProviderType, secrets.Envelope{Ciphertext: stored.Ciphertext, Nonce: stored.Nonce, KeyVersion: stored.KeyVersion})
	if err != nil {
		return nil, "", err
	}
	defer clear(plaintext)
	provider, err := service.registry.Create(providers.ConnectionConfig{ID: connection.ID, Type: connection.ProviderType, Endpoint: connection.Endpoint, Settings: connection.Settings, Credentials: plaintext})
	return provider, connection.ProviderType, err
}

func trustedMockPage(providerType string, target *url.URL) (string, bool) {
	if providerType != "mock" || target == nil || target.Scheme != "mock+page" || target.User != nil || target.Fragment != "" || target.RawQuery != "" || (target.Path != "" && target.Path != "/") || target.Port() != "" {
		return "", false
	}
	switch target.Hostname() {
	case "console":
		return "/api/v1/mock-pages/console", true
	case "portal":
		return "/api/v1/mock-pages/portal", true
	default:
		return "", false
	}
}

func (service *Service) appendAudit(ctx context.Context, targetID, eventType, requestID, sourceIP string, metadata map[string]string) error {
	encoded, _ := json.Marshal(metadata)
	return service.audit.Append(ctx, audit.Entry{ID: service.options.NewAuditID(), EventType: eventType, TargetType: "console", TargetID: targetID, RequestID: requestID, SourceIP: sourceIP, Metadata: encoded, CreatedAt: service.options.Now().UTC()})
}

func optionFromCapability(capability providers.Capability) Option {
	return Option{Available: capability.Available, Reason: capability.Reason}
}

func randomTicket() string {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		panic("secure random source unavailable")
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}
