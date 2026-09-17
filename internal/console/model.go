package console

import "time"

type Mode string

const ModeEmbedded Mode = "embedded"

type Result string

const (
	ResultPending   Result = "pending"
	ResultActive    Result = "active"
	ResultCompleted Result = "completed"
	ResultFailed    Result = "failed"
	ResultExpired   Result = "expired"
)

type Session struct {
	ID         string     `json:"id"`
	ServerID   string     `json:"server_id"`
	Mode       Mode       `json:"mode"`
	TicketHash []byte     `json:"-"`
	ExpiresAt  time.Time  `json:"expires_at"`
	OpenedAt   *time.Time `json:"opened_at"`
	ClosedAt   *time.Time `json:"closed_at"`
	Result     Result     `json:"result"`
	CreatedAt  time.Time  `json:"created_at"`
}
