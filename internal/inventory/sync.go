package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"

	"controlpanel/internal/connections"
	"controlpanel/internal/providers"
	"controlpanel/internal/secrets"
	"github.com/google/uuid"
)

const (
	maximumSyncPages   = 1000
	maximumSyncServers = 10000
)

var ErrConnectionDisabled = errors.New("provider connection is disabled")

type ConnectionRepository interface {
	FindByID(context.Context, string) (connections.Connection, connections.CredentialRecord, error)
	UpdateHealth(context.Context, string, connections.HealthStatus, string, string, time.Time) error
}

type SyncerOptions struct {
	Now   func() time.Time
	NewID func() string
}

type Syncer struct {
	connections ConnectionRepository
	inventory   Repository
	cipher      *secrets.CredentialCipher
	registry    *providers.Registry
	now         func() time.Time
	newID       func() string
}

func NewSyncer(connectionRepository ConnectionRepository, inventoryRepository Repository, cipher *secrets.CredentialCipher, registry *providers.Registry, options SyncerOptions) *Syncer {
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	if options.NewID == nil {
		options.NewID = uuid.NewString
	}
	return &Syncer{connections: connectionRepository, inventory: inventoryRepository, cipher: cipher, registry: registry, now: options.Now, newID: options.NewID}
}

func (syncer *Syncer) SyncConnection(ctx context.Context, connectionID string) error {
	connection, storedCredentials, err := syncer.connections.FindByID(ctx, connectionID)
	if err != nil {
		return err
	}
	if !connection.Enabled {
		return ErrConnectionDisabled
	}
	plaintext, err := syncer.cipher.Decrypt(connection.ID, connection.ProviderType, secrets.Envelope{
		Ciphertext: storedCredentials.Ciphertext, Nonce: storedCredentials.Nonce, KeyVersion: storedCredentials.KeyVersion,
	})
	if err != nil {
		return fmt.Errorf("decrypt provider credentials: %w", err)
	}
	defer clear(plaintext)
	provider, err := syncer.registry.Create(providers.ConnectionConfig{
		ID: connection.ID, Type: connection.ProviderType, Endpoint: connection.Endpoint,
		Settings: connection.Settings, Credentials: plaintext,
	})
	if err != nil {
		return syncer.recordFailure(ctx, connection.ID, err)
	}
	if _, err := provider.ValidateConnection(ctx); err != nil {
		return syncer.recordFailure(ctx, connection.ID, err)
	}

	completedAt := syncer.now().UTC()
	servers := make([]Server, 0)
	var cursor *providers.Cursor
	for pageNumber := 0; pageNumber < maximumSyncPages; pageNumber++ {
		page, err := provider.ListServers(ctx, cursor)
		if err != nil {
			return syncer.recordFailure(ctx, connection.ID, err)
		}
		if len(servers)+len(page.Servers) > maximumSyncServers {
			return syncer.recordFailure(ctx, connection.ID, &providers.Error{Code: providers.ErrorProvider, Message: "provider inventory exceeds 10000 servers"})
		}
		for _, remote := range page.Servers {
			server, err := syncer.normalizeServer(ctx, provider, connection.ID, remote, completedAt)
			if err != nil {
				return syncer.recordFailure(ctx, connection.ID, err)
			}
			servers = append(servers, server)
		}
		if page.Next == nil {
			return syncer.inventory.ApplyCompleteSync(ctx, SyncSnapshot{ConnectionID: connection.ID, CompletedAt: completedAt, Servers: servers})
		}
		cursor = page.Next
	}
	return syncer.recordFailure(ctx, connection.ID, &providers.Error{Code: providers.ErrorProvider, Message: "provider pagination exceeds 1000 pages"})
}

func (syncer *Syncer) normalizeServer(ctx context.Context, provider providers.Provider, connectionID string, remote providers.RemoteServer, at time.Time) (Server, error) {
	if remote.ExternalID == "" || remote.Name == "" {
		return Server{}, errors.New("provider returned an invalid server")
	}
	capabilities, err := json.Marshal(remote.Capabilities)
	if err != nil {
		return Server{}, errors.New("encode server capabilities")
	}
	state := State(remote.State)
	if !validState(state) {
		state = StateUnknown
	}
	var portalURL *string
	if remote.Capabilities.HasProviderPortal.Available {
		target, err := provider.ProviderPortalURL(ctx, providers.ServerRef{ExternalID: remote.ExternalID, Scope: remote.Scope})
		if err != nil {
			return Server{}, err
		}
		if err := validatePortalURL(target); err != nil {
			return Server{}, err
		}
		value := target.String()
		portalURL = &value
	}
	return Server{
		ID: syncer.newID(), ConnectionID: connectionID, ExternalID: remote.ExternalID, Scope: remote.Scope,
		Name: remote.Name, State: state, RemoteState: remote.RemoteState,
		Spec: append(json.RawMessage(nil), remote.Spec...), Addresses: append(json.RawMessage(nil), remote.Addresses...),
		Capabilities: capabilities, PortalURL: portalURL, LastSeenAt: at, LastStateCheckedAt: at, CreatedAt: at, UpdatedAt: at,
	}, nil
}

func (syncer *Syncer) recordFailure(ctx context.Context, connectionID string, syncErr error) error {
	status, code, message := classifyProviderError(syncErr)
	if err := syncer.connections.UpdateHealth(ctx, connectionID, status, code, message, syncer.now().UTC()); err != nil {
		return errors.Join(syncErr, err)
	}
	return syncErr
}

func classifyProviderError(err error) (connections.HealthStatus, string, string) {
	var providerError *providers.Error
	if !errors.As(err, &providerError) {
		return connections.HealthOffline, string(providers.ErrorProvider), "Provider synchronization failed"
	}
	status := connections.HealthOffline
	if providerError.Retryable {
		status = connections.HealthDegraded
	}
	return status, string(providerError.Code), providerError.Message
}

func validatePortalURL(target *url.URL) error {
	if target == nil || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") {
		return errors.New("provider returned an invalid portal URL")
	}
	return nil
}

func validState(state State) bool {
	switch state {
	case StatePending, StateRunning, StateStopping, StateStopped, StateRebooting, StateSuspended, StateError, StateUnknown:
		return true
	default:
		return false
	}
}
