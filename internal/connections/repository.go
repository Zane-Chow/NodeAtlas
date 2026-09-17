package connections

import (
	"context"
	"errors"
	"time"
)

var ErrNotFound = errors.New("provider connection not found")

type Repository interface {
	Create(context.Context, Connection, CredentialRecord) error
	List(context.Context) ([]Connection, error)
	FindByID(context.Context, string) (Connection, CredentialRecord, error)
	Update(context.Context, Connection, *CredentialRecord) error
	UpdateHealth(context.Context, string, HealthStatus, string, string, time.Time) error
	SoftDelete(context.Context, string, time.Time) error
}
