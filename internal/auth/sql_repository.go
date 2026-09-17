package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"controlpanel/internal/database"
	mysqldriver "github.com/go-sql-driver/mysql"
)

type SQLRepository struct {
	db      *sql.DB
	dialect database.Dialect
}

func NewSQLRepository(db *sql.DB, dialect database.Dialect) *SQLRepository {
	return &SQLRepository{db: db, dialect: dialect}
}

func (repository *SQLRepository) IsInitialized(ctx context.Context) (bool, error) {
	var count int
	if err := repository.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		return false, fmt.Errorf("count users: %w", err)
	}
	return count > 0, nil
}

func (repository *SQLRepository) Initialize(ctx context.Context, user User) error {
	now := user.InitializedAt.UTC()
	_, err := repository.db.ExecContext(ctx, `INSERT INTO users
		(singleton_key, id, username, normalized_username, password_hash, initialized_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		1, user.ID, user.Username, user.NormalizedUsername, user.PasswordHash,
		repository.timeValue(now), repository.timeValue(now), repository.timeValue(now),
	)
	if isDuplicateError(err) {
		return ErrAlreadyInitialized
	}
	if err != nil {
		return fmt.Errorf("initialize administrator: %w", err)
	}
	return nil
}

func (repository *SQLRepository) FindUserByNormalizedUsername(ctx context.Context, username string) (User, error) {
	return repository.findUser(ctx, `SELECT id, username, normalized_username, password_hash, initialized_at, last_login_at
		FROM users WHERE normalized_username = ?`, username)
}

func (repository *SQLRepository) FindUserByID(ctx context.Context, id string) (User, error) {
	return repository.findUser(ctx, `SELECT id, username, normalized_username, password_hash, initialized_at, last_login_at
		FROM users WHERE id = ?`, id)
}

func (repository *SQLRepository) findUser(ctx context.Context, query string, argument any) (User, error) {
	var user User
	var initialized string
	var lastLogin sql.NullString
	err := repository.db.QueryRowContext(ctx, query, argument).Scan(
		&user.ID, &user.Username, &user.NormalizedUsername, &user.PasswordHash, &initialized, &lastLogin,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("find user: %w", err)
	}
	user.InitializedAt, err = parseDatabaseTime(initialized)
	if err != nil {
		return User{}, err
	}
	if lastLogin.Valid {
		parsed, parseErr := parseDatabaseTime(lastLogin.String)
		if parseErr != nil {
			return User{}, parseErr
		}
		user.LastLoginAt = &parsed
	}
	return user, nil
}

func (repository *SQLRepository) UpdateLastLogin(ctx context.Context, userID string, at time.Time) error {
	_, err := repository.db.ExecContext(ctx, "UPDATE users SET last_login_at = ?, updated_at = ? WHERE id = ?", repository.timeValue(at), repository.timeValue(at), userID)
	return err
}

func (repository *SQLRepository) CreateSession(ctx context.Context, session Session) error {
	_, err := repository.db.ExecContext(ctx, `INSERT INTO sessions
		(token_hash, csrf_hash, user_id, expires_at, absolute_expires_at, last_seen_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		session.TokenHash[:], session.CSRFHash[:], session.UserID,
		repository.timeValue(session.ExpiresAt), repository.timeValue(session.AbsoluteExpiresAt),
		repository.timeValue(session.LastSeenAt), repository.timeValue(session.CreatedAt),
	)
	return err
}

func (repository *SQLRepository) FindSessionByTokenHash(ctx context.Context, hash [32]byte) (Session, error) {
	var session Session
	var token, csrf []byte
	var expires, absolute, lastSeen, created string
	err := repository.db.QueryRowContext(ctx, `SELECT token_hash, csrf_hash, user_id, expires_at,
		absolute_expires_at, last_seen_at, created_at FROM sessions WHERE token_hash = ?`, hash[:]).Scan(
		&token, &csrf, &session.UserID, &expires, &absolute, &lastSeen, &created,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("find session: %w", err)
	}
	if len(token) != 32 || len(csrf) != 32 {
		return Session{}, errors.New("invalid stored session hash")
	}
	copy(session.TokenHash[:], token)
	copy(session.CSRFHash[:], csrf)
	for target, value := range map[*time.Time]string{
		&session.ExpiresAt: expires, &session.AbsoluteExpiresAt: absolute,
		&session.LastSeenAt: lastSeen, &session.CreatedAt: created,
	} {
		parsed, parseErr := parseDatabaseTime(value)
		if parseErr != nil {
			return Session{}, parseErr
		}
		*target = parsed
	}
	return session, nil
}

func (repository *SQLRepository) TouchSession(ctx context.Context, hash [32]byte, lastSeen, expires time.Time) error {
	_, err := repository.db.ExecContext(ctx, "UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE token_hash = ?", repository.timeValue(lastSeen), repository.timeValue(expires), hash[:])
	return err
}

func (repository *SQLRepository) DeleteSession(ctx context.Context, hash [32]byte) error {
	_, err := repository.db.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash = ?", hash[:])
	return err
}

func (repository *SQLRepository) RevokeOtherSessions(ctx context.Context, userID string, keep [32]byte) error {
	_, err := repository.db.ExecContext(ctx, "DELETE FROM sessions WHERE user_id = ? AND token_hash <> ?", userID, keep[:])
	return err
}

func (repository *SQLRepository) UpdatePasswordAndRevokeSessions(ctx context.Context, userID, passwordHash string, keep [32]byte) error {
	transaction, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = transaction.Rollback() }()
	if _, err := transaction.ExecContext(ctx, "UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?", passwordHash, repository.timeValue(time.Now().UTC()), userID); err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, "DELETE FROM sessions WHERE user_id = ? AND token_hash <> ?", userID, keep[:]); err != nil {
		return err
	}
	return transaction.Commit()
}

func (repository *SQLRepository) timeValue(value time.Time) any {
	if repository.dialect == database.DialectMySQL {
		return value.UTC()
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func parseDatabaseTime(value string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999", "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, errors.New("invalid database time")
}

func isDuplicateError(err error) bool {
	if err == nil {
		return false
	}
	var mysqlError *mysqldriver.MySQLError
	if errors.As(err, &mysqlError) && mysqlError.Number == 1062 {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint") || strings.Contains(message, "duplicate entry")
}
