package mock

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"controlpanel/internal/providers"
)

type Factory struct {
	mutex       sync.Mutex
	connections map[string]*sharedState
}

type settings struct {
	ServerCount      int     `json:"server_count"`
	Seed             int64   `json:"seed"`
	OperationDelayMS int     `json:"operation_delay_ms"`
	FailureRate      float64 `json:"failure_rate"`
	HealthMode       string  `json:"health_mode"`
	ConsoleProfile   string  `json:"console_profile"`
}

type credentials struct {
	Token string `json:"token"`
}

type Provider struct {
	connectionID string
	settings     settings
	state        *sharedState
}

type sharedState struct {
	mutex       sync.Mutex
	fingerprint string
	random      *rand.Rand
	servers     []providers.RemoteServer
}

func NewFactory() *Factory {
	return &Factory{connections: make(map[string]*sharedState)}
}

func (factory *Factory) Create(config providers.ConnectionConfig) (providers.Provider, error) {
	if strings.TrimSpace(config.ID) == "" {
		return nil, errors.New("mock connection ID is required")
	}
	configuration := settings{ServerCount: 4, Seed: 1, HealthMode: "healthy", ConsoleProfile: "embedded"}
	if len(config.Settings) > 0 {
		if err := decodeStrict(config.Settings, &configuration); err != nil {
			return nil, fmt.Errorf("invalid mock settings: %w", err)
		}
	}
	if configuration.ServerCount < 1 || configuration.ServerCount > 500 {
		return nil, errors.New("mock server_count must be between 1 and 500")
	}
	if configuration.OperationDelayMS < 0 || configuration.OperationDelayMS > 30_000 {
		return nil, errors.New("mock operation_delay_ms must be between 0 and 30000")
	}
	if configuration.FailureRate < 0 || configuration.FailureRate > 1 {
		return nil, errors.New("mock failure_rate must be between 0 and 1")
	}
	if !contains([]string{"healthy", "authentication_failure", "network_failure", "rate_limited"}, configuration.HealthMode) {
		return nil, errors.New("unsupported mock health_mode")
	}
	if !contains([]string{"embedded", "window", "portal", "none"}, configuration.ConsoleProfile) {
		return nil, errors.New("unsupported mock console_profile")
	}
	var secret credentials
	if err := decodeStrict(config.Credentials, &secret); err != nil || strings.TrimSpace(secret.Token) == "" {
		return nil, errors.New("mock token is required")
	}
	fingerprintBytes, _ := json.Marshal(configuration)
	fingerprint := string(fingerprintBytes)
	factory.mutex.Lock()
	defer factory.mutex.Unlock()
	if factory.connections == nil {
		factory.connections = make(map[string]*sharedState)
	}
	state, exists := factory.connections[config.ID]
	if !exists || state.fingerprint != fingerprint {
		state = &sharedState{fingerprint: fingerprint, random: rand.New(rand.NewSource(configuration.Seed))}
		provider := &Provider{connectionID: config.ID, settings: configuration, state: state}
		state.servers = provider.generateServers()
		factory.connections[config.ID] = state
	}
	provider := &Provider{connectionID: config.ID, settings: configuration, state: state}
	return provider, nil
}

func (provider *Provider) ValidateConnection(context.Context) (providers.ConnectionInfo, error) {
	switch provider.settings.HealthMode {
	case "authentication_failure":
		return providers.ConnectionInfo{}, &providers.Error{Code: providers.ErrorAuthentication, Message: "mock credentials rejected"}
	case "network_failure":
		return providers.ConnectionInfo{}, &providers.Error{Code: providers.ErrorNetwork, Message: "mock network unavailable", Retryable: true}
	case "rate_limited":
		return providers.ConnectionInfo{}, &providers.Error{Code: providers.ErrorRateLimited, Message: "mock rate limit reached", Retryable: true}
	default:
		return providers.ConnectionInfo{DisplayName: "Mock Lab " + provider.connectionID, Version: "1"}, nil
	}
}

func (provider *Provider) ListServers(_ context.Context, cursor *providers.Cursor) (providers.ServerPage, error) {
	provider.state.mutex.Lock()
	defer provider.state.mutex.Unlock()
	start := 0
	if cursor != nil {
		parsed, err := strconv.Atoi(cursor.Value)
		if err != nil || parsed < 0 || parsed >= len(provider.state.servers) {
			return providers.ServerPage{}, &providers.Error{Code: providers.ErrorInvalidConfig, Message: "invalid mock cursor"}
		}
		start = parsed
	}
	end := min(start+100, len(provider.state.servers))
	page := providers.ServerPage{Servers: append([]providers.RemoteServer(nil), provider.state.servers[start:end]...)}
	if end < len(provider.state.servers) {
		page.Next = &providers.Cursor{Value: strconv.Itoa(end)}
	}
	return page, nil
}

func (provider *Provider) GetServer(_ context.Context, ref providers.ServerRef) (providers.RemoteServer, error) {
	provider.state.mutex.Lock()
	defer provider.state.mutex.Unlock()
	index := provider.findServer(ref)
	if index < 0 {
		return providers.RemoteServer{}, &providers.Error{Code: providers.ErrorNotFound, Message: "mock server not found"}
	}
	return provider.state.servers[index], nil
}

func (provider *Provider) StartServer(ctx context.Context, ref providers.ServerRef) (providers.ActionReceipt, error) {
	return provider.changeState(ctx, ref, providers.StateRunning)
}

func (provider *Provider) StopServer(ctx context.Context, ref providers.ServerRef) (providers.ActionReceipt, error) {
	return provider.changeState(ctx, ref, providers.StateStopped)
}

func (provider *Provider) RebootServer(ctx context.Context, ref providers.ServerRef) (providers.ActionReceipt, error) {
	return provider.changeState(ctx, ref, providers.StateRunning)
}

func (provider *Provider) OpenConsole(_ context.Context, ref providers.ServerRef, mode providers.ConsoleMode) (providers.ConsoleTarget, error) {
	server, err := provider.GetServer(context.Background(), ref)
	if err != nil {
		return providers.ConsoleTarget{}, err
	}
	allowed := mode == providers.ConsoleEmbedded && provider.settings.ConsoleProfile == "embedded"
	allowed = allowed || mode == providers.ConsoleWindow && (provider.settings.ConsoleProfile == "embedded" || provider.settings.ConsoleProfile == "window")
	if !allowed {
		return providers.ConsoleTarget{}, &providers.Error{Code: providers.ErrorUnsupported, Message: "mock console mode unavailable"}
	}
	scheme := "https"
	host := "mock.invalid"
	if mode == providers.ConsoleEmbedded {
		scheme = "mock+ws"
		host = "console"
	}
	target, _ := url.Parse(scheme + "://" + host + "/console/" + url.PathEscape(provider.connectionID) + "/" + url.PathEscape(server.ExternalID))
	return providers.ConsoleTarget{Mode: mode, URL: target}, nil
}

func (provider *Provider) ProviderPortalURL(_ context.Context, ref providers.ServerRef) (*url.URL, error) {
	if provider.settings.ConsoleProfile == "none" {
		return nil, &providers.Error{Code: providers.ErrorUnsupported, Message: "mock portal unavailable"}
	}
	server, err := provider.GetServer(context.Background(), ref)
	if err != nil {
		return nil, err
	}
	return url.Parse("https://mock.invalid/connections/" + url.PathEscape(provider.connectionID) + "/servers/" + url.PathEscape(server.ExternalID))
}

func (provider *Provider) changeState(ctx context.Context, ref providers.ServerRef, state providers.ServerState) (providers.ActionReceipt, error) {
	if provider.settings.OperationDelayMS > 0 {
		timer := time.NewTimer(time.Duration(provider.settings.OperationDelayMS) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return providers.ActionReceipt{}, ctx.Err()
		case <-timer.C:
		}
	}
	provider.state.mutex.Lock()
	defer provider.state.mutex.Unlock()
	if provider.state.random.Float64() < provider.settings.FailureRate {
		return providers.ActionReceipt{}, &providers.Error{Code: providers.ErrorProvider, Message: "mock operation failed", Retryable: true}
	}
	index := provider.findServer(ref)
	if index < 0 {
		return providers.ActionReceipt{}, &providers.Error{Code: providers.ErrorNotFound, Message: "mock server not found"}
	}
	provider.state.servers[index].State = state
	provider.state.servers[index].RemoteState = strings.ToUpper(string(state))
	provider.state.servers[index].Capabilities = provider.capabilities(state)
	return providers.ActionReceipt{RequestID: fmt.Sprintf("mock-%d", provider.state.random.Int63())}, nil
}

func (provider *Provider) findServer(ref providers.ServerRef) int {
	for index := range provider.state.servers {
		if provider.state.servers[index].ExternalID == ref.ExternalID && provider.state.servers[index].Scope == ref.Scope {
			return index
		}
	}
	return -1
}

func (provider *Provider) generateServers() []providers.RemoteServer {
	states := []providers.ServerState{providers.StateRunning, providers.StateStopped, providers.StatePending, providers.StateError}
	servers := make([]providers.RemoteServer, 0, provider.settings.ServerCount)
	for index := 1; index <= provider.settings.ServerCount; index++ {
		state := states[(index-1+int(provider.settings.Seed))%len(states)]
		externalID := fmt.Sprintf("%s-server-%03d", provider.connectionID, index)
		spec, _ := json.Marshal(map[string]any{"cpu": 1 + index%4, "memory_mb": 1024 * (1 + index%8)})
		addresses, _ := json.Marshal([]map[string]string{{"type": "private", "address": fmt.Sprintf("10.%d.%d.%d", provider.settings.Seed%250, index/250, index%250+1)}})
		servers = append(servers, providers.RemoteServer{
			ExternalID: externalID, Scope: fmt.Sprintf("mock-zone-%d", index%3+1), Name: fmt.Sprintf("mock-node-%02d", index),
			State: state, RemoteState: strings.ToUpper(string(state)), Spec: spec, Addresses: addresses,
			Capabilities: provider.capabilities(state),
		})
	}
	return servers
}

func (provider *Provider) capabilities(state providers.ServerState) providers.Capabilities {
	capabilities := providers.Capabilities{
		CanStart:  providers.Capability{Available: state == providers.StateStopped, Reason: "server must be stopped"},
		CanStop:   providers.Capability{Available: state == providers.StateRunning, Reason: "server must be running"},
		CanReboot: providers.Capability{Available: state == providers.StateRunning, Reason: "server must be running"},
	}
	if capabilities.CanStart.Available {
		capabilities.CanStart.Reason = ""
	}
	if capabilities.CanStop.Available {
		capabilities.CanStop.Reason = ""
	}
	if capabilities.CanReboot.Available {
		capabilities.CanReboot.Reason = ""
	}
	switch provider.settings.ConsoleProfile {
	case "embedded":
		capabilities.CanEmbedConsole.Available = true
		capabilities.CanOpenConsoleWindow.Available = true
		capabilities.HasProviderPortal.Available = true
	case "window":
		capabilities.CanEmbedConsole.Reason = "provider only supports a new window"
		capabilities.CanOpenConsoleWindow.Available = true
		capabilities.HasProviderPortal.Available = true
	case "portal":
		capabilities.CanEmbedConsole.Reason = "provider console embedding unavailable"
		capabilities.CanOpenConsoleWindow.Reason = "provider temporary console URL unavailable"
		capabilities.HasProviderPortal.Available = true
	case "none":
		capabilities.CanEmbedConsole.Reason = "console unavailable"
		capabilities.CanOpenConsoleWindow.Reason = "console unavailable"
		capabilities.HasProviderPortal.Reason = "provider portal unavailable"
	}
	return capabilities
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("expected one JSON value")
	}
	return nil
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
