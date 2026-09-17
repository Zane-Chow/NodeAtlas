package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
)

type PasswordHasher interface {
	Hash(string) (string, error)
	Verify(string, string) (bool, error)
}

type ServiceOptions struct {
	Now           func() time.Time
	Random        io.Reader
	IdleTTL       time.Duration
	AbsoluteTTL   time.Duration
	TouchInterval time.Duration
}

type AuthenticatedSession struct {
	User    User
	Session Session
}

type Service struct {
	repository    Repository
	hasher        PasswordHasher
	now           func() time.Time
	random        io.Reader
	idleTTL       time.Duration
	absoluteTTL   time.Duration
	touchInterval time.Duration
}

func NewService(repository Repository, hasher PasswordHasher, options ServiceOptions) *Service {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	if options.IdleTTL == 0 {
		options.IdleTTL = 12 * time.Hour
	}
	if options.AbsoluteTTL == 0 {
		options.AbsoluteTTL = 7 * 24 * time.Hour
	}
	if options.TouchInterval == 0 {
		options.TouchInterval = 5 * time.Minute
	}
	return &Service{
		repository: repository, hasher: hasher, now: options.Now, random: options.Random,
		idleTTL: options.IdleTTL, absoluteTTL: options.AbsoluteTTL, touchInterval: options.TouchInterval,
	}
}

func (service *Service) SetupStatus(ctx context.Context) (bool, error) {
	return service.repository.IsInitialized(ctx)
}

func (service *Service) Initialize(ctx context.Context, username, password string) error {
	displayName := strings.TrimSpace(username)
	if displayName == "" || len([]byte(displayName)) > 255 {
		return errors.New("username must contain 1 to 255 bytes")
	}
	passwordHash, err := service.hasher.Hash(password)
	if err != nil {
		return err
	}
	now := service.now().UTC()
	return service.repository.Initialize(ctx, User{
		ID:                 uuid.NewString(),
		Username:           displayName,
		NormalizedUsername: normalizeUsername(displayName),
		PasswordHash:       passwordHash,
		InitializedAt:      now,
	})
}

func (service *Service) Login(ctx context.Context, username, password string) (SessionGrant, error) {
	user, err := service.repository.FindUserByNormalizedUsername(ctx, normalizeUsername(username))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return SessionGrant{}, ErrInvalidCredentials
		}
		return SessionGrant{}, err
	}
	valid, err := service.hasher.Verify(user.PasswordHash, password)
	if err != nil || !valid {
		return SessionGrant{}, ErrInvalidCredentials
	}
	grant, session, err := service.newSession(user)
	if err != nil {
		return SessionGrant{}, err
	}
	if err := service.repository.CreateSession(ctx, session); err != nil {
		return SessionGrant{}, err
	}
	if err := service.repository.UpdateLastLogin(ctx, user.ID, service.now().UTC()); err != nil {
		_ = service.repository.DeleteSession(ctx, session.TokenHash)
		return SessionGrant{}, err
	}
	return grant, nil
}

func (service *Service) Authenticate(ctx context.Context, rawToken string) (AuthenticatedSession, error) {
	if rawToken == "" {
		return AuthenticatedSession{}, ErrInvalidSession
	}
	hash := sha256.Sum256([]byte(rawToken))
	session, err := service.repository.FindSessionByTokenHash(ctx, hash)
	if err != nil {
		return AuthenticatedSession{}, ErrInvalidSession
	}
	now := service.now().UTC()
	if !now.Before(session.ExpiresAt) || !now.Before(session.AbsoluteExpiresAt) {
		_ = service.repository.DeleteSession(ctx, hash)
		return AuthenticatedSession{}, ErrInvalidSession
	}
	user, err := service.repository.FindUserByID(ctx, session.UserID)
	if err != nil {
		return AuthenticatedSession{}, ErrInvalidSession
	}
	if now.Sub(session.LastSeenAt) >= service.touchInterval {
		expiresAt := now.Add(service.idleTTL)
		if expiresAt.After(session.AbsoluteExpiresAt) {
			expiresAt = session.AbsoluteExpiresAt
		}
		if err := service.repository.TouchSession(ctx, hash, now, expiresAt); err != nil {
			return AuthenticatedSession{}, err
		}
		session.LastSeenAt = now
		session.ExpiresAt = expiresAt
	}
	return AuthenticatedSession{User: user, Session: session}, nil
}

func (service *Service) Logout(ctx context.Context, rawToken string) error {
	if rawToken == "" {
		return nil
	}
	return service.repository.DeleteSession(ctx, sha256.Sum256([]byte(rawToken)))
}

func (service *Service) ChangePassword(ctx context.Context, rawToken, currentPassword, newPassword string) error {
	authenticated, err := service.Authenticate(ctx, rawToken)
	if err != nil {
		return err
	}
	valid, err := service.hasher.Verify(authenticated.User.PasswordHash, currentPassword)
	if err != nil || !valid {
		return ErrInvalidCurrentPassword
	}
	encoded, err := service.hasher.Hash(newPassword)
	if err != nil {
		return err
	}
	return service.repository.UpdatePasswordAndRevokeSessions(ctx, authenticated.User.ID, encoded, authenticated.Session.TokenHash)
}

func (service *Service) newSession(user User) (SessionGrant, Session, error) {
	rawSession := make([]byte, 32)
	rawCSRF := make([]byte, 32)
	if _, err := io.ReadFull(service.random, rawSession); err != nil {
		return SessionGrant{}, Session{}, err
	}
	if _, err := io.ReadFull(service.random, rawCSRF); err != nil {
		return SessionGrant{}, Session{}, err
	}
	sessionToken := base64.RawURLEncoding.EncodeToString(rawSession)
	csrfToken := base64.RawURLEncoding.EncodeToString(rawCSRF)
	now := service.now().UTC()
	absolute := now.Add(service.absoluteTTL)
	return SessionGrant{
		SessionToken: sessionToken,
		CSRFToken:    csrfToken,
		ExpiresAt:    now.Add(service.idleTTL),
		User:         user,
	}, Session{
		TokenHash:         sha256.Sum256([]byte(sessionToken)),
		CSRFHash:          sha256.Sum256([]byte(csrfToken)),
		UserID:            user.ID,
		ExpiresAt:         now.Add(service.idleTTL),
		AbsoluteExpiresAt: absolute,
		LastSeenAt:        now,
		CreatedAt:         now,
	}, nil
}

func normalizeUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}
