package inventory

import (
	"encoding/json"
	"time"
)

type State string

const (
	StatePending   State = "pending"
	StateRunning   State = "running"
	StateStopping  State = "stopping"
	StateStopped   State = "stopped"
	StateRebooting State = "rebooting"
	StateSuspended State = "suspended"
	StateError     State = "error"
	StateUnknown   State = "unknown"
)

type Server struct {
	ID                 string          `json:"id"`
	ConnectionID       string          `json:"connection_id"`
	ExternalID         string          `json:"external_id"`
	Scope              string          `json:"scope"`
	Name               string          `json:"name"`
	State              State           `json:"state"`
	RemoteState        string          `json:"remote_state"`
	Spec               json.RawMessage `json:"spec"`
	Addresses          json.RawMessage `json:"addresses"`
	Capabilities       json.RawMessage `json:"capabilities"`
	PortalURL          *string         `json:"portal_url,omitempty"`
	LastSeenAt         time.Time       `json:"last_seen_at"`
	LastStateCheckedAt time.Time       `json:"last_state_checked_at"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
}

type SyncSnapshot struct {
	ConnectionID string
	CompletedAt  time.Time
	Servers      []Server
}

type Filter struct {
	ConnectionID string
	ProviderType string
	State        State
	Query        string
}
