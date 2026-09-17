package backup

import (
	"encoding/json"
	"time"
)

type Kind string

const (
	KindManual Kind = "manual"
	KindSafety Kind = "safety"
)

type Status string

const (
	StatusReady  Status = "ready"
	StatusFailed Status = "failed"
)

type Metadata struct {
	ID            string          `json:"id"`
	Filename      string          `json:"filename"`
	SizeBytes     int64           `json:"size_bytes"`
	SHA256        string          `json:"sha256"`
	FormatVersion int             `json:"format_version"`
	Kind          Kind            `json:"kind"`
	Status        Status          `json:"status"`
	Manifest      json.RawMessage `json:"manifest"`
	CreatedAt     time.Time       `json:"created_at"`
}
