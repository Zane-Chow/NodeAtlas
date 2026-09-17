package audit

import (
	"encoding/json"
	"time"
)

type Entry struct {
	ID         string          `json:"id"`
	EventType  string          `json:"event_type"`
	TargetType string          `json:"target_type"`
	TargetID   string          `json:"target_id"`
	RequestID  string          `json:"request_id,omitempty"`
	SourceIP   string          `json:"source_ip,omitempty"`
	Metadata   json.RawMessage `json:"metadata"`
	CreatedAt  time.Time       `json:"created_at"`
}

type Filter struct {
	EventType  string
	TargetType string
	TargetID   string
}
