// Package pvepanel is a deliberately hypothetical provider adapter example.
//
// It does not implement the Proxmox VE API and is not compatible with any real
// vendor panel. Copy it into a uniquely named package, replace the Client
// boundary with that vendor's documented protocol, add network-policy checks,
// and register the resulting factory explicitly in the application.
package pvepanel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strings"

	"controlpanel/internal/providers"
)

const ProviderType = "example-pvepanel"

type settings struct {
	Tenant string `json:"tenant"`
}

type credentials struct {
	Token string `json:"token"`
}

// PanelInfo, VendorPage, and VendorServer are example transport DTOs. A real
// adapter must define these from one vendor's documented response schema.
type PanelInfo struct {
	Name    string
	Version string
}

type VendorPage struct {
	Servers    []VendorServer
	NextCursor string
}

type VendorServer struct {
	ID             string
	Cluster        string
	Name           string
	Status         string
	VCPUs          int
	MemoryMB       int
	Addresses      []string
	ConsoleEnabled bool
}

type PowerAction string

const (
	PowerStart  PowerAction = "start"
	PowerStop   PowerAction = "stop"
	PowerReboot PowerAction = "reboot"
)

type ConsoleSession struct {
	Protocol string
	URL      *url.URL
	Password string
}

// Client isolates vendor response parsing and authenticated HTTP from the core
// Provider contract. Its real implementation must use providers/network for
// endpoint, redirect, DNS, dial, TLS, timeout, and response-size enforcement.
type Client interface {
	Info(context.Context) (PanelInfo, error)
	List(context.Context, string, string) (VendorPage, error)
	Get(context.Context, string, providers.ServerRef) (VendorServer, error)
	Power(context.Context, string, providers.ServerRef, PowerAction) (string, error)
	Console(context.Context, string, providers.ServerRef, providers.ConsoleMode) (ConsoleSession, error)
	Portal(context.Context, string, providers.ServerRef) (*url.URL, error)
}

// ClientFactory receives the validated origin and write-only token. The token
// must never be placed in URLs, errors, inventory records, or browser payloads.
type ClientFactory func(endpoint *url.URL, token string) (Client, error)

type Factory struct {
	newClient ClientFactory
}

type Provider struct {
	tenant string
	client Client
}

var _ providers.Factory = (*Factory)(nil)
var _ providers.Provider = (*Provider)(nil)

func NewFactory(newClient ClientFactory) *Factory {
	return &Factory{newClient: newClient}
}

func (factory *Factory) Create(config providers.ConnectionConfig) (providers.Provider, error) {
	if factory == nil || factory.newClient == nil {
		return nil, errors.New("example PVE panel client factory is required")
	}
	if strings.TrimSpace(config.ID) == "" {
		return nil, errors.New("example PVE panel connection ID is required")
	}
	endpoint, err := parseEndpoint(config.Endpoint)
	if err != nil {
		return nil, errors.New("invalid example PVE panel endpoint")
	}
	var configuration settings
	if err := decodeStrict(config.Settings, &configuration); err != nil || strings.TrimSpace(configuration.Tenant) == "" {
		return nil, errors.New("invalid example PVE panel settings")
	}
	configuration.Tenant = strings.TrimSpace(configuration.Tenant)
	var secret credentials
	if err := decodeStrict(config.Credentials, &secret); err != nil || strings.TrimSpace(secret.Token) == "" {
		return nil, errors.New("example PVE panel token is required")
	}
	client, err := factory.newClient(endpoint, strings.TrimSpace(secret.Token))
	if err != nil {
		return nil, errors.New("example PVE panel client could not be created")
	}
	if client == nil {
		return nil, errors.New("example PVE panel client is required")
	}
	return &Provider{tenant: configuration.Tenant, client: client}, nil
}

func (provider *Provider) ValidateConnection(ctx context.Context) (providers.ConnectionInfo, error) {
	info, err := provider.client.Info(ctx)
	if err != nil {
		return providers.ConnectionInfo{}, err
	}
	return providers.ConnectionInfo{DisplayName: info.Name, Version: info.Version}, nil
}

func (provider *Provider) ListServers(ctx context.Context, cursor *providers.Cursor) (providers.ServerPage, error) {
	value := ""
	if cursor != nil {
		value = cursor.Value
	}
	page, err := provider.client.List(ctx, provider.tenant, value)
	if err != nil {
		return providers.ServerPage{}, err
	}
	result := providers.ServerPage{Servers: make([]providers.RemoteServer, 0, len(page.Servers))}
	for _, server := range page.Servers {
		mapped, mapErr := mapServer(server)
		if mapErr != nil {
			return providers.ServerPage{}, mapErr
		}
		result.Servers = append(result.Servers, mapped)
	}
	if strings.TrimSpace(page.NextCursor) != "" {
		result.Next = &providers.Cursor{Value: strings.TrimSpace(page.NextCursor)}
	}
	return result, nil
}

func (provider *Provider) GetServer(ctx context.Context, ref providers.ServerRef) (providers.RemoteServer, error) {
	server, err := provider.client.Get(ctx, provider.tenant, ref)
	if err != nil {
		return providers.RemoteServer{}, err
	}
	return mapServer(server)
}

func (provider *Provider) StartServer(ctx context.Context, ref providers.ServerRef) (providers.ActionReceipt, error) {
	return provider.power(ctx, ref, PowerStart)
}

func (provider *Provider) StopServer(ctx context.Context, ref providers.ServerRef) (providers.ActionReceipt, error) {
	return provider.power(ctx, ref, PowerStop)
}

func (provider *Provider) RebootServer(ctx context.Context, ref providers.ServerRef) (providers.ActionReceipt, error) {
	return provider.power(ctx, ref, PowerReboot)
}

func (provider *Provider) power(ctx context.Context, ref providers.ServerRef, action PowerAction) (providers.ActionReceipt, error) {
	requestID, err := provider.client.Power(ctx, provider.tenant, ref, action)
	if err != nil {
		return providers.ActionReceipt{}, err
	}
	return providers.ActionReceipt{RequestID: requestID}, nil
}

func (provider *Provider) OpenConsole(ctx context.Context, ref providers.ServerRef, mode providers.ConsoleMode) (providers.ConsoleTarget, error) {
	if mode != providers.ConsoleEmbedded && mode != providers.ConsoleWindow {
		return providers.ConsoleTarget{}, &providers.Error{Code: providers.ErrorInvalidConfig, Message: "example console mode is invalid"}
	}
	session, err := provider.client.Console(ctx, provider.tenant, ref, mode)
	if err != nil {
		return providers.ConsoleTarget{}, err
	}
	return providers.ConsoleTarget{Mode: mode, Protocol: session.Protocol, URL: session.URL, Password: session.Password}, nil
}

func (provider *Provider) ProviderPortalURL(ctx context.Context, ref providers.ServerRef) (*url.URL, error) {
	return provider.client.Portal(ctx, provider.tenant, ref)
}

func mapServer(server VendorServer) (providers.RemoteServer, error) {
	if strings.TrimSpace(server.ID) == "" || strings.TrimSpace(server.Cluster) == "" {
		return providers.RemoteServer{}, &providers.Error{Code: providers.ErrorProvider, Message: "example provider returned incomplete server identity"}
	}
	state := normalizeState(server.Status)
	spec, err := json.Marshal(struct {
		VCPUs    int `json:"vcpus"`
		MemoryMB int `json:"memory_mb"`
	}{VCPUs: server.VCPUs, MemoryMB: server.MemoryMB})
	if err != nil {
		return providers.RemoteServer{}, &providers.Error{Code: providers.ErrorProvider, Message: "example provider returned invalid server spec"}
	}
	addresses, err := json.Marshal(server.Addresses)
	if err != nil {
		return providers.RemoteServer{}, &providers.Error{Code: providers.ErrorProvider, Message: "example provider returned invalid server addresses"}
	}
	return providers.RemoteServer{
		ExternalID:   strings.TrimSpace(server.ID),
		Scope:        strings.TrimSpace(server.Cluster),
		Name:         strings.TrimSpace(server.Name),
		State:        state,
		RemoteState:  strings.TrimSpace(server.Status),
		Spec:         spec,
		Addresses:    addresses,
		Capabilities: capabilities(state, server.ConsoleEnabled),
	}, nil
}

func normalizeState(status string) providers.ServerState {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running":
		return providers.StateRunning
	case "stopped":
		return providers.StateStopped
	case "pending":
		return providers.StatePending
	case "stopping":
		return providers.StateStopping
	case "rebooting":
		return providers.StateRebooting
	case "suspended":
		return providers.StateSuspended
	case "error":
		return providers.StateError
	default:
		return providers.StateUnknown
	}
}

func capabilities(state providers.ServerState, consoleEnabled bool) providers.Capabilities {
	result := providers.Capabilities{
		CanStart:             unavailable("server must be stopped"),
		CanStop:              unavailable("server must be running"),
		CanReboot:            unavailable("server must be running"),
		CanEmbedConsole:      unavailable("console is unavailable"),
		CanOpenConsoleWindow: unavailable("console is unavailable"),
		HasProviderPortal:    providers.Capability{Available: true},
	}
	if state == providers.StateStopped {
		result.CanStart = providers.Capability{Available: true}
	}
	if state == providers.StateRunning {
		result.CanStop = providers.Capability{Available: true}
		result.CanReboot = providers.Capability{Available: true}
		if consoleEnabled {
			result.CanEmbedConsole = providers.Capability{Available: true}
			result.CanOpenConsoleWindow = providers.Capability{Available: true}
		}
	}
	return result
}

func unavailable(reason string) providers.Capability {
	return providers.Capability{Reason: reason}
}

func parseEndpoint(raw string) (*url.URL, error) {
	endpoint, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil ||
		endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" ||
		(endpoint.Path != "" && endpoint.Path != "/") {
		return nil, errors.New("invalid endpoint")
	}
	endpoint.Path = ""
	return endpoint, nil
}

func decodeStrict(data []byte, target any) error {
	if len(data) == 0 {
		return errors.New("JSON value is required")
	}
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
