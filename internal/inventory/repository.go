package inventory

import (
	"context"
	"errors"
)

var ErrNotFound = errors.New("server not found")

type Repository interface {
	ApplyCompleteSync(context.Context, SyncSnapshot) error
	List(context.Context, Filter) ([]Server, error)
	FindByID(context.Context, string) (Server, error)
}
