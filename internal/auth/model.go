package auth

import (
	"errors"
	"time"
)

var (
	ErrAlreadyInitialized     = errors.New("administrator already initialized")
	ErrNotFound               = errors.New("not found")
	ErrInvalidCredentials     = errors.New("invalid username or password")
	ErrInvalidSession         = errors.New("invalid session")
	ErrInvalidCurrentPassword = errors.New("current password is invalid")
)

type User struct {
	ID                 string
	Username           string
	NormalizedUsername string
	PasswordHash       string
	InitializedAt      time.Time
	LastLoginAt        *time.Time
}

type Session struct {
	TokenHash         [32]byte
	CSRFHash          [32]byte
	UserID            string
	ExpiresAt         time.Time
	AbsoluteExpiresAt time.Time
	LastSeenAt        time.Time
	CreatedAt         time.Time
}

type SessionGrant struct {
	SessionToken string
	CSRFToken    string
	ExpiresAt    time.Time
	User         User
}
