package backup

import (
	"context"
	"errors"
)

var ErrNotFound = errors.New("backup not found")

type Repository interface {
	Create(context.Context, Metadata) error
	FindByID(context.Context, string) (Metadata, error)
	List(context.Context) ([]Metadata, error)
}
