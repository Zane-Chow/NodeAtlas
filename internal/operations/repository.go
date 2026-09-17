package operations

import (
	"context"
	"errors"
)

var (
	ErrNotFound          = errors.New("operation not found")
	ErrActiveOperation   = errors.New("server already has an active operation")
	ErrInvalidTransition = errors.New("invalid operation transition")
)

type Repository interface {
	CreateQueued(context.Context, Operation) (Operation, bool, error)
	FindByID(context.Context, string) (Operation, error)
	FindByIdempotencyKey(context.Context, string) (Operation, error)
	List(context.Context, Filter) ([]Operation, error)
	Transition(context.Context, string, Status, Transition) (Operation, error)
}
