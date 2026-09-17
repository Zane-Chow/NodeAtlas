package connections

import (
	"encoding/json"
	"time"
)

type HealthStatus string

const (
	HealthUnknown  HealthStatus = "unknown"
	HealthHealthy  HealthStatus = "healthy"
	HealthDegraded HealthStatus = "degraded"
	HealthOffline  HealthStatus = "offline"
)

type Connection struct {
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	ProviderType     string          `json:"provider_type"`
	Endpoint         string          `json:"endpoint"`
	Settings         json.RawMessage `json:"settings"`
	Enabled          bool            `json:"enabled"`
	HealthStatus     HealthStatus    `json:"health_status"`
	LastTestedAt     *time.Time      `json:"last_tested_at"`
	LastSyncedAt     *time.Time      `json:"last_synced_at"`
	LastErrorCode    string          `json:"last_error_code,omitempty"`
	LastErrorMessage string          `json:"last_error_message,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

type CredentialRecord struct {
	Ciphertext []byte
	Nonce      []byte
	KeyVersion int
}
