package jobs

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNoJob     = errors.New("no job available")
	ErrNotFound  = errors.New("job not found")
	ErrLeaseLost = errors.New("job lease lost")
)

type Repository interface {
	Enqueue(context.Context, Job) error
	LeaseNext(context.Context, string, time.Time, time.Duration) (Lease, error)
	Retry(context.Context, string, string, time.Time, string) error
	Complete(context.Context, string, string, time.Time) error
	Fail(context.Context, string, string, time.Time, string) error
	FindByID(context.Context, string) (Job, error)
}
