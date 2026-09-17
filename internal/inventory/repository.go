package inventory

import (
	"context"
	"errors"
	"time"

	"controlpanel/internal/providers"
)

var ErrNotFound = errors.New("server not found")

type Repository interface {
	ApplyCompleteSync(context.Context, SyncSnapshot) error
	List(context.Context, Filter) ([]Server, error)
	FindByID(context.Context, string) (Server, error)
	UpdateRemote(context.Context, string, providers.RemoteServer, time.Time) error
}
