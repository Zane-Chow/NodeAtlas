package console

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound          = errors.New("console session not found")
	ErrTicketUnavailable = errors.New("console ticket is unavailable")
)

type Repository interface {
	Create(context.Context, Session) error
	ConsumeTicket(context.Context, []byte, time.Time) (Session, error)
	FindByID(context.Context, string) (Session, error)
	Close(context.Context, string, Result, time.Time) error
}
