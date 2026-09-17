package connections

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"controlpanel/internal/database"
)

type SQLRepository struct {
	db      *sql.DB
	dialect database.Dialect
}

func NewSQLRepository(db *sql.DB, dialect database.Dialect) *SQLRepository {
	return &SQLRepository{db: db, dialect: dialect}
}

func (repository *SQLRepository) Create(ctx context.Context, connection Connection, credentials CredentialRecord) error {
	_, err := repository.db.ExecContext(ctx, `INSERT INTO provider_connections
		(id, name, provider_type, endpoint, settings_json, credentials_ciphertext, credentials_nonce,
		 credentials_key_version, enabled, health_status, last_error_code, last_error_message, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		connection.ID, connection.Name, connection.ProviderType, connection.Endpoint, string(connection.Settings),
		credentials.Ciphertext, credentials.Nonce, credentials.KeyVersion, connection.Enabled, connection.HealthStatus,
		connection.LastErrorCode, connection.LastErrorMessage, repository.timeValue(connection.CreatedAt), repository.timeValue(connection.UpdatedAt))
	if err != nil {
		return fmt.Errorf("create provider connection: %w", err)
	}
	return nil
}

func (repository *SQLRepository) List(ctx context.Context) ([]Connection, error) {
	rows, err := repository.db.QueryContext(ctx, `SELECT id, name, provider_type, endpoint, settings_json,
		enabled, health_status, last_tested_at, last_synced_at, last_error_code, last_error_message, created_at, updated_at
		FROM provider_connections WHERE deleted_at IS NULL ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list provider connections: %w", err)
	}
	defer rows.Close()
	connections := make([]Connection, 0)
	for rows.Next() {
		connection, err := scanConnection(rows.Scan)
		if err != nil {
			return nil, err
		}
		connections = append(connections, connection)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list provider connections: %w", err)
	}
	return connections, nil
}

func (repository *SQLRepository) FindByID(ctx context.Context, id string) (Connection, CredentialRecord, error) {
	var credentials CredentialRecord
	row := repository.db.QueryRowContext(ctx, `SELECT id, name, provider_type, endpoint, settings_json,
		enabled, health_status, last_tested_at, last_synced_at, last_error_code, last_error_message, created_at, updated_at,
		credentials_ciphertext, credentials_nonce, credentials_key_version
		FROM provider_connections WHERE id = ? AND deleted_at IS NULL`, id)
	connection, err := scanConnection(func(dest ...any) error {
		return row.Scan(append(dest, &credentials.Ciphertext, &credentials.Nonce, &credentials.KeyVersion)...)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Connection{}, CredentialRecord{}, ErrNotFound
	}
	if err != nil {
		return Connection{}, CredentialRecord{}, fmt.Errorf("find provider connection: %w", err)
	}
	return connection, credentials, nil
}

func (repository *SQLRepository) Update(ctx context.Context, connection Connection, credentials *CredentialRecord) error {
	query := `UPDATE provider_connections SET name = ?, endpoint = ?, settings_json = ?, enabled = ?, updated_at = ?`
	arguments := []any{connection.Name, connection.Endpoint, string(connection.Settings), connection.Enabled, repository.timeValue(connection.UpdatedAt)}
	if credentials != nil {
		query += `, credentials_ciphertext = ?, credentials_nonce = ?, credentials_key_version = ?`
		arguments = append(arguments, credentials.Ciphertext, credentials.Nonce, credentials.KeyVersion)
	}
	query += ` WHERE id = ? AND deleted_at IS NULL`
	arguments = append(arguments, connection.ID)
	result, err := repository.db.ExecContext(ctx, query, arguments...)
	if err != nil {
		return fmt.Errorf("update provider connection: %w", err)
	}
	return requireAffected(result)
}

func (repository *SQLRepository) UpdateHealth(ctx context.Context, id string, health HealthStatus, code, message string, testedAt time.Time) error {
	result, err := repository.db.ExecContext(ctx, `UPDATE provider_connections
		SET health_status = ?, last_error_code = ?, last_error_message = ?, last_tested_at = ?, updated_at = ?
		WHERE id = ? AND deleted_at IS NULL`, health, code, message, repository.timeValue(testedAt), repository.timeValue(testedAt), id)
	if err != nil {
		return fmt.Errorf("update provider connection health: %w", err)
	}
	return requireAffected(result)
}

func (repository *SQLRepository) SoftDelete(ctx context.Context, id string, deletedAt time.Time) error {
	result, err := repository.db.ExecContext(ctx, `UPDATE provider_connections SET enabled = ?, deleted_at = ?, updated_at = ?
		WHERE id = ? AND deleted_at IS NULL`, false, repository.timeValue(deletedAt), repository.timeValue(deletedAt), id)
	if err != nil {
		return fmt.Errorf("delete provider connection: %w", err)
	}
	return requireAffected(result)
}

type scanFunc func(...any) error

func scanConnection(scan scanFunc) (Connection, error) {
	var connection Connection
	var settings, created, updated string
	var tested, synced sql.NullString
	err := scan(&connection.ID, &connection.Name, &connection.ProviderType, &connection.Endpoint, &settings,
		&connection.Enabled, &connection.HealthStatus, &tested, &synced, &connection.LastErrorCode,
		&connection.LastErrorMessage, &created, &updated)
	if err != nil {
		return Connection{}, err
	}
	connection.Settings = []byte(settings)
	var parseErr error
	if connection.CreatedAt, parseErr = parseDatabaseTime(created); parseErr != nil {
		return Connection{}, parseErr
	}
	if connection.UpdatedAt, parseErr = parseDatabaseTime(updated); parseErr != nil {
		return Connection{}, parseErr
	}
	if tested.Valid {
		value, err := parseDatabaseTime(tested.String)
		if err != nil {
			return Connection{}, err
		}
		connection.LastTestedAt = &value
	}
	if synced.Valid {
		value, err := parseDatabaseTime(synced.String)
		if err != nil {
			return Connection{}, err
		}
		connection.LastSyncedAt = &value
	}
	return connection, nil
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

func requireAffected(result sql.Result) error {
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}
