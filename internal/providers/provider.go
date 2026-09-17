package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

type ServerState string

const (
	StatePending   ServerState = "pending"
	StateRunning   ServerState = "running"
	StateStopping  ServerState = "stopping"
	StateStopped   ServerState = "stopped"
	StateRebooting ServerState = "rebooting"
	StateSuspended ServerState = "suspended"
	StateError     ServerState = "error"
	StateUnknown   ServerState = "unknown"
)

type ErrorCode string

const (
	ErrorAuthentication ErrorCode = "authentication_failed"
	ErrorPermission     ErrorCode = "permission_denied"
	ErrorRateLimited    ErrorCode = "rate_limited"
	ErrorNetwork        ErrorCode = "network_error"
	ErrorInvalidConfig  ErrorCode = "invalid_configuration"
	ErrorNotFound       ErrorCode = "not_found"
	ErrorUnsupported    ErrorCode = "unsupported"
	ErrorProvider       ErrorCode = "provider_error"
)

type Error struct {
	Code      ErrorCode
	Message   string
	Retryable bool
}

func (providerError *Error) Error() string {
	return fmt.Sprintf("provider %s: %s", providerError.Code, providerError.Message)
}

type ConnectionConfig struct {
	ID          string
	Type        string
	Endpoint    string
	Settings    json.RawMessage
	Credentials json.RawMessage
}

type ConnectionInfo struct {
	DisplayName string `json:"display_name"`
	Version     string `json:"version"`
}

type Cursor struct {
	Value string `json:"value"`
}

type ServerRef struct {
	ExternalID string
	Scope      string
}

type Capability struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

type Capabilities struct {
	CanStart             Capability `json:"can_start"`
	CanStop              Capability `json:"can_stop"`
	CanReboot            Capability `json:"can_reboot"`
	CanEmbedConsole      Capability `json:"can_embed_console"`
	CanOpenConsoleWindow Capability `json:"can_open_console_window"`
	HasProviderPortal    Capability `json:"has_provider_portal"`
}

type RemoteServer struct {
	ExternalID   string          `json:"external_id"`
	Scope        string          `json:"scope"`
	Name         string          `json:"name"`
	State        ServerState     `json:"state"`
	RemoteState  string          `json:"remote_state"`
	Spec         json.RawMessage `json:"spec"`
	Addresses    json.RawMessage `json:"addresses"`
	Capabilities Capabilities    `json:"capabilities"`
}

type ServerPage struct {
	Servers []RemoteServer `json:"servers"`
	Next    *Cursor        `json:"next,omitempty"`
}

type ActionReceipt struct {
	RequestID string `json:"request_id"`
}

type ConsoleMode string

const (
	ConsoleEmbedded ConsoleMode = "embedded"
	ConsoleWindow   ConsoleMode = "window"
)

type ConsoleTarget struct {
	Mode ConsoleMode `json:"mode"`
	URL  *url.URL    `json:"-"`
}

type Provider interface {
	ValidateConnection(context.Context) (ConnectionInfo, error)
	ListServers(context.Context, *Cursor) (ServerPage, error)
	GetServer(context.Context, ServerRef) (RemoteServer, error)
	StartServer(context.Context, ServerRef) (ActionReceipt, error)
	StopServer(context.Context, ServerRef) (ActionReceipt, error)
	RebootServer(context.Context, ServerRef) (ActionReceipt, error)
	OpenConsole(context.Context, ServerRef, ConsoleMode) (ConsoleTarget, error)
	ProviderPortalURL(context.Context, ServerRef) (*url.URL, error)
}

type Factory interface {
	Create(ConnectionConfig) (Provider, error)
}
