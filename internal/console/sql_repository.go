package console

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"controlpanel/internal/database"
)

const sessionColumns = `id, server_id, mode, ticket_hash, expires_at, opened_at, closed_at, result, created_at`

type SQLRepository struct {
	db      *sql.DB
	dialect database.Dialect
}

func NewSQLRepository(db *sql.DB, dialect database.Dialect) *SQLRepository {
	return &SQLRepository{db: db, dialect: dialect}
}

func (repository *SQLRepository) Create(ctx context.Context, session Session) error {
	_, err := repository.db.ExecContext(ctx, `INSERT INTO console_sessions
		(id, server_id, mode, ticket_hash, expires_at, opened_at, closed_at, result, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, session.ID, session.ServerID, session.Mode, session.TicketHash,
		repository.timeValue(session.ExpiresAt), repository.nullTime(session.OpenedAt), repository.nullTime(session.ClosedAt),
		session.Result, repository.timeValue(session.CreatedAt))
	if err != nil {
		return fmt.Errorf("create console session: %w", err)
	}
	return nil
}

func (repository *SQLRepository) ConsumeTicket(ctx context.Context, ticketHash []byte, openedAt time.Time) (Session, error) {
	transaction, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, err
	}
	defer func() { _ = transaction.Rollback() }()
	session, err := findSession(ctx, transaction, `WHERE ticket_hash = ?`, ticketHash)
	if err != nil || session.OpenedAt != nil || session.ClosedAt != nil || !openedAt.Before(session.ExpiresAt) {
		return Session{}, ErrTicketUnavailable
	}
	result, err := transaction.ExecContext(ctx, `UPDATE console_sessions SET opened_at = ?, result = ?
		WHERE id = ? AND opened_at IS NULL AND closed_at IS NULL AND expires_at > ?`,
		repository.timeValue(openedAt), ResultActive, session.ID, repository.timeValue(openedAt))
	if err != nil {
		return Session{}, err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return Session{}, ErrTicketUnavailable
	}
	openedAt = openedAt.UTC()
	session.OpenedAt = &openedAt
	session.Result = ResultActive
	if err := transaction.Commit(); err != nil {
		return Session{}, err
	}
	return session, nil
}

func (repository *SQLRepository) FindByID(ctx context.Context, id string) (Session, error) {
	return findSession(ctx, repository.db, `WHERE id = ?`, id)
}

func (repository *SQLRepository) Close(ctx context.Context, id string, result Result, closedAt time.Time) error {
	updated, err := repository.db.ExecContext(ctx, `UPDATE console_sessions SET closed_at = ?, result = ? WHERE id = ? AND closed_at IS NULL`,
		repository.timeValue(closedAt), result, id)
	if err != nil {
		return err
	}
	if count, _ := updated.RowsAffected(); count != 1 {
		return ErrNotFound
	}
	return nil
}

type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func findSession(ctx context.Context, queryer queryRower, clause string, argument any) (Session, error) {
	var session Session
	var expiresAt, createdAt string
	var openedAt, closedAt sql.NullString
	err := queryer.QueryRowContext(ctx, `SELECT `+sessionColumns+` FROM console_sessions `+clause, argument).Scan(
		&session.ID, &session.ServerID, &session.Mode, &session.TicketHash, &expiresAt, &openedAt, &closedAt, &session.Result, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, err
	}
	if session.ExpiresAt, err = parseTime(expiresAt); err != nil {
		return Session{}, err
	}
	if session.CreatedAt, err = parseTime(createdAt); err != nil {
		return Session{}, err
	}
	if openedAt.Valid {
		value, parseErr := parseTime(openedAt.String)
		if parseErr != nil {
			return Session{}, parseErr
		}
		session.OpenedAt = &value
	}
	if closedAt.Valid {
		value, parseErr := parseTime(closedAt.String)
		if parseErr != nil {
			return Session{}, parseErr
		}
		session.ClosedAt = &value
	}
	return session, nil
}

func (repository *SQLRepository) timeValue(value time.Time) any {
	if repository.dialect == database.DialectMySQL {
		return value.UTC()
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func (repository *SQLRepository) nullTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return repository.timeValue(*value)
}

func parseTime(value string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999", "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, errors.New("invalid database time")
}
