package operations

import "time"

type Action string

const (
	ActionStart  Action = "start"
	ActionStop   Action = "stop"
	ActionReboot Action = "reboot"
)

type Status string

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusVerifying Status = "verifying"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusTimedOut  Status = "timed_out"
	StatusCancelled Status = "cancelled"
)

type Operation struct {
	ID                string     `json:"id"`
	ServerID          string     `json:"server_id"`
	ConnectionID      string     `json:"connection_id"`
	Action            Action     `json:"action"`
	Status            Status     `json:"status"`
	IdempotencyKey    string     `json:"-"`
	ProviderRequestID string     `json:"provider_request_id,omitempty"`
	ErrorCode         string     `json:"error_code,omitempty"`
	ErrorMessage      string     `json:"error_message,omitempty"`
	QueuedAt          time.Time  `json:"queued_at"`
	StartedAt         *time.Time `json:"started_at"`
	FinishedAt        *time.Time `json:"finished_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type Filter struct {
	ServerID     string
	ConnectionID string
	Status       Status
}

type Transition struct {
	At                time.Time
	ProviderRequestID string
	ErrorCode         string
	ErrorMessage      string
}

func (status Status) terminal() bool {
	switch status {
	case StatusSucceeded, StatusFailed, StatusTimedOut, StatusCancelled:
		return true
	default:
		return false
	}
}
