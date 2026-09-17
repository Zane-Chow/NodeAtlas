package jobs

import (
	"encoding/json"
	"time"
)

type Kind string

const (
	KindSyncConnection Kind = "sync_connection"
	KindPowerOperation Kind = "power_operation"
)

type Status string

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
)

type Job struct {
	ID             string
	Kind           Kind
	Payload        json.RawMessage
	Status         Status
	Attempts       int
	MaxAttempts    int
	AvailableAt    time.Time
	LeaseOwner     string
	LeaseExpiresAt *time.Time
	LastError      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}
type Lease struct {
	Job       Job
	Owner     string
	ExpiresAt time.Time
}
