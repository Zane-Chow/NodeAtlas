package connections

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"controlpanel/internal/providers"
	"controlpanel/internal/secrets"
	"github.com/google/uuid"
)

var ErrInvalidConnection = errors.New("invalid provider connection")

type SyncQueue interface {
	EnqueueConnectionSync(context.Context, string) error
}

type CreateInput struct {
	Name         string
	ProviderType string
	Endpoint     string
	Settings     json.RawMessage
	Credentials  json.RawMessage
	Enabled      bool
}

type TestResult struct {
	Healthy     bool   `json:"healthy"`
	Code        string `json:"code,omitempty"`
	Message     string `json:"message"`
	DisplayName string `json:"display_name,omitempty"`
	Version     string `json:"version,omitempty"`
}

type ServiceOptions struct {
	Now   func() time.Time
	NewID func() string
}

type Service struct {
	repository Repository
	cipher     *secrets.CredentialCipher
	registry   *providers.Registry
	queue      SyncQueue
	now        func() time.Time
	newID      func() string
}

func NewService(repository Repository, cipher *secrets.CredentialCipher, registry *providers.Registry, queue SyncQueue, options ServiceOptions) *Service {
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	if options.NewID == nil {
		options.NewID = uuid.NewString
	}
	return &Service{repository: repository, cipher: cipher, registry: registry, queue: queue, now: options.Now, newID: options.NewID}
}

func (service *Service) Create(ctx context.Context, input CreateInput) (Connection, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.ProviderType = strings.ToLower(strings.TrimSpace(input.ProviderType))
	input.Endpoint = strings.TrimSpace(input.Endpoint)
	if input.Name == "" || len(input.Name) > 255 || input.ProviderType == "" {
		return Connection{}, ErrInvalidConnection
	}
	if len(input.Settings) == 0 {
		input.Settings = json.RawMessage(`{}`)
	}
	if !json.Valid(input.Settings) || !json.Valid(input.Credentials) {
		return Connection{}, ErrInvalidConnection
	}

	id := service.newID()
	credentials := append([]byte(nil), input.Credentials...)
	defer clear(credentials)
	if _, err := service.registry.Create(providers.ConnectionConfig{
		ID: id, Type: input.ProviderType, Endpoint: input.Endpoint, Settings: input.Settings, Credentials: credentials,
	}); err != nil {
		return Connection{}, fmt.Errorf("%w: %v", ErrInvalidConnection, err)
	}
	envelope, err := service.cipher.Encrypt(id, input.ProviderType, credentials)
	if err != nil {
		return Connection{}, fmt.Errorf("encrypt provider credentials: %w", err)
	}
	now := service.now().UTC()
	connection := Connection{
		ID: id, Name: input.Name, ProviderType: input.ProviderType, Endpoint: input.Endpoint,
		Settings: append(json.RawMessage(nil), input.Settings...), Enabled: input.Enabled, HealthStatus: HealthUnknown,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := service.repository.Create(ctx, connection, CredentialRecord{
		Ciphertext: envelope.Ciphertext, Nonce: envelope.Nonce, KeyVersion: envelope.KeyVersion,
	}); err != nil {
		return Connection{}, err
	}
	if connection.Enabled && service.queue != nil {
		if err := service.queue.EnqueueConnectionSync(ctx, connection.ID); err != nil {
			return Connection{}, fmt.Errorf("queue initial provider sync: %w", err)
		}
	}
	return connection, nil
}

func (service *Service) List(ctx context.Context) ([]Connection, error) {
	return service.repository.List(ctx)
}

func (service *Service) Find(ctx context.Context, id string) (Connection, error) {
	connection, _, err := service.repository.FindByID(ctx, id)
	return connection, err
}

func (service *Service) Test(ctx context.Context, id string) (TestResult, error) {
	connection, stored, err := service.repository.FindByID(ctx, id)
	if err != nil {
		return TestResult{}, err
	}
	plaintext, err := service.cipher.Decrypt(connection.ID, connection.ProviderType, secrets.Envelope{
		Ciphertext: stored.Ciphertext, Nonce: stored.Nonce, KeyVersion: stored.KeyVersion,
	})
	if err != nil {
		return TestResult{}, fmt.Errorf("decrypt provider credentials: %w", err)
	}
	defer clear(plaintext)
	provider, err := service.registry.Create(providers.ConnectionConfig{
		ID: connection.ID, Type: connection.ProviderType, Endpoint: connection.Endpoint,
		Settings: connection.Settings, Credentials: plaintext,
	})
	if err != nil {
		return TestResult{}, fmt.Errorf("construct provider: %w", err)
	}
	info, validationErr := provider.ValidateConnection(ctx)
	testedAt := service.now().UTC()
	if validationErr == nil {
		if err := service.repository.UpdateHealth(ctx, id, HealthHealthy, "", "", testedAt); err != nil {
			return TestResult{}, err
		}
		return TestResult{Healthy: true, Message: "Connection succeeded", DisplayName: info.DisplayName, Version: info.Version}, nil
	}
	status, code, message := classifyProviderError(validationErr)
	if err := service.repository.UpdateHealth(ctx, id, status, code, message, testedAt); err != nil {
		return TestResult{}, err
	}
	return TestResult{Healthy: false, Code: code, Message: message}, nil
}

func classifyProviderError(err error) (HealthStatus, string, string) {
	var providerError *providers.Error
	if !errors.As(err, &providerError) {
		return HealthOffline, string(providers.ErrorProvider), "Provider connection failed"
	}
	status := HealthOffline
	if providerError.Retryable {
		status = HealthDegraded
	}
	return status, string(providerError.Code), providerError.Message
}
