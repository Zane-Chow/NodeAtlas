package auth

import (
	"context"
	"time"
)

type Repository interface {
	IsInitialized(context.Context) (bool, error)
	Initialize(context.Context, User) error
	FindUserByNormalizedUsername(context.Context, string) (User, error)
	FindUserByID(context.Context, string) (User, error)
	UpdateLastLogin(context.Context, string, time.Time) error
	CreateSession(context.Context, Session) error
	FindSessionByTokenHash(context.Context, [32]byte) (Session, error)
	TouchSession(context.Context, [32]byte, time.Time, time.Time) error
	DeleteSession(context.Context, [32]byte) error
	RevokeOtherSessions(context.Context, string, [32]byte) error
	UpdatePasswordAndRevokeSessions(context.Context, string, string, [32]byte) error
}
