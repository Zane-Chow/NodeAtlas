package operations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"controlpanel/internal/database"
)

const operationColumns = `id, server_id, connection_id, action, status, idempotency_key,
	provider_request_id, error_code, error_message, queued_at, started_at, finished_at, updated_at`

type SQLRepository struct {
	db      *sql.DB
	dialect database.Dialect
}

func NewSQLRepository(db *sql.DB, dialect database.Dialect) *SQLRepository {
	return &SQLRepository{db: db, dialect: dialect}
}

func (repository *SQLRepository) CreateQueued(ctx context.Context, operation Operation) (Operation, bool, error) {
	transaction, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return Operation{}, false, err
	}
	defer func() { _ = transaction.Rollback() }()
	if existing, err := findOperation(ctx, transaction, `WHERE idempotency_key = ?`, operation.IdempotencyKey); err == nil {
		return existing, false, nil
	} else if !errors.Is(err, ErrNotFound) {
		return Operation{}, false, err
	}
	operation.Status = StatusQueued
	_, err = transaction.ExecContext(ctx, `INSERT INTO operations
		(id, server_id, connection_id, action, status, idempotency_key, active_marker, provider_request_id,
		 error_code, error_message, queued_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, 'active', ?, ?, ?, ?, ?)`,
		operation.ID, operation.ServerID, operation.ConnectionID, operation.Action, operation.Status,
		operation.IdempotencyKey, operation.ProviderRequestID, operation.ErrorCode, operation.ErrorMessage,
		repository.timeValue(operation.QueuedAt), repository.timeValue(operation.UpdatedAt))
	if err != nil {
		if existing, findErr := findOperation(ctx, transaction, `WHERE idempotency_key = ?`, operation.IdempotencyKey); findErr == nil {
			return existing, false, nil
		}
		if _, findErr := findOperation(ctx, transaction, `WHERE server_id = ? AND active_marker = 'active'`, operation.ServerID); findErr == nil {
			return Operation{}, false, ErrActiveOperation
		}
		return Operation{}, false, fmt.Errorf("create operation: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return Operation{}, false, err
	}
	return operation, true, nil
}

func (repository *SQLRepository) FindByID(ctx context.Context, id string) (Operation, error) {
	return findOperation(ctx, repository.db, `WHERE id = ?`, id)
}

func (repository *SQLRepository) FindByIdempotencyKey(ctx context.Context, key string) (Operation, error) {
	return findOperation(ctx, repository.db, `WHERE idempotency_key = ?`, key)
}

func (repository *SQLRepository) List(ctx context.Context, filter Filter) ([]Operation, error) {
	conditions := make([]string, 0, 3)
	arguments := make([]any, 0, 3)
	if filter.ServerID != "" {
		conditions, arguments = append(conditions, "server_id = ?"), append(arguments, filter.ServerID)
	}
	if filter.ConnectionID != "" {
		conditions, arguments = append(conditions, "connection_id = ?"), append(arguments, filter.ConnectionID)
	}
	if filter.Status != "" {
		conditions, arguments = append(conditions, "status = ?"), append(arguments, filter.Status)
	}
	query := `SELECT ` + operationColumns + ` FROM operations`
	if len(conditions) > 0 {
		query += ` WHERE ` + strings.Join(conditions, ` AND `)
	}
	query += ` ORDER BY queued_at DESC, id DESC`
	rows, err := repository.db.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list operations: %w", err)
	}
	defer rows.Close()
	result := make([]Operation, 0)
	for rows.Next() {
		operation, err := scanOperation(rows.Scan)
		if err != nil {
			return nil, err
		}
		result = append(result, operation)
	}
	return result, rows.Err()
}

func (repository *SQLRepository) Transition(ctx context.Context, id string, next Status, change Transition) (Operation, error) {
	transaction, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return Operation{}, err
	}
	defer func() { _ = transaction.Rollback() }()
	current, err := findOperation(ctx, transaction, `WHERE id = ?`, id)
	if err != nil {
		return Operation{}, err
	}
	if !validTransition(current.Status, next) {
		return Operation{}, ErrInvalidTransition
	}
	previous := current.Status
	at := change.At.UTC()
	if at.IsZero() {
		at = time.Now().UTC()
	}
	current.Status = next
	current.UpdatedAt = at
	if next == StatusRunning && current.StartedAt == nil {
		current.StartedAt = &at
	}
	if next.terminal() {
		current.FinishedAt = &at
	}
	if change.ProviderRequestID != "" {
		current.ProviderRequestID = change.ProviderRequestID
	}
	current.ErrorCode, current.ErrorMessage = change.ErrorCode, change.ErrorMessage
	var marker any = "active"
	if next.terminal() {
		marker = nil
	}
	result, err := transaction.ExecContext(ctx, `UPDATE operations SET status = ?, active_marker = ?,
		provider_request_id = ?, error_code = ?, error_message = ?, started_at = ?, finished_at = ?, updated_at = ?
		WHERE id = ? AND status = ?`, current.Status, marker, current.ProviderRequestID, current.ErrorCode,
		current.ErrorMessage, repository.nullTime(current.StartedAt), repository.nullTime(current.FinishedAt),
		repository.timeValue(current.UpdatedAt), current.ID, previous)
	if err != nil {
		return Operation{}, fmt.Errorf("transition operation: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return Operation{}, ErrInvalidTransition
	}
	if err := transaction.Commit(); err != nil {
		return Operation{}, err
	}
	return current, nil
}

func validTransition(current, next Status) bool {
	switch current {
	case StatusQueued:
		return next == StatusRunning || next == StatusFailed || next == StatusCancelled
	case StatusRunning:
		return next == StatusVerifying || next == StatusSucceeded || next == StatusFailed || next == StatusTimedOut
	case StatusVerifying:
		return next == StatusSucceeded || next == StatusFailed || next == StatusTimedOut
	default:
		return false
	}
}

type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func findOperation(ctx context.Context, queryer queryRower, clause string, argument any) (Operation, error) {
	operation, err := scanOperation(queryer.QueryRowContext(ctx, `SELECT `+operationColumns+` FROM operations `+clause, argument).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return Operation{}, ErrNotFound
	}
	if err != nil {
		return Operation{}, fmt.Errorf("find operation: %w", err)
	}
	return operation, nil
}

type scanFunc func(...any) error

func scanOperation(scan scanFunc) (Operation, error) {
	var operation Operation
	var queued, updated string
	var started, finished sql.NullString
	if err := scan(&operation.ID, &operation.ServerID, &operation.ConnectionID, &operation.Action, &operation.Status,
		&operation.IdempotencyKey, &operation.ProviderRequestID, &operation.ErrorCode, &operation.ErrorMessage,
		&queued, &started, &finished, &updated); err != nil {
		return Operation{}, err
	}
	var err error
	if operation.QueuedAt, err = parseTime(queued); err != nil {
		return Operation{}, err
	}
	if operation.UpdatedAt, err = parseTime(updated); err != nil {
		return Operation{}, err
	}
	if started.Valid {
		value, err := parseTime(started.String)
		if err != nil {
			return Operation{}, err
		}
		operation.StartedAt = &value
	}
	if finished.Valid {
		value, err := parseTime(finished.String)
		if err != nil {
			return Operation{}, err
		}
		operation.FinishedAt = &value
	}
	return operation, nil
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
