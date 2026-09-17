package inventory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
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

func (repository *SQLRepository) ApplyCompleteSync(ctx context.Context, snapshot SyncSnapshot) error {
	transaction, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin inventory sync: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	at := repository.timeValue(snapshot.CompletedAt)
	if _, err := transaction.ExecContext(ctx, `UPDATE servers SET hidden_at = ?, updated_at = ? WHERE connection_id = ? AND hidden_at IS NULL`, at, at, snapshot.ConnectionID); err != nil {
		return fmt.Errorf("hide stale servers: %w", err)
	}
	for _, server := range snapshot.Servers {
		if server.ConnectionID != snapshot.ConnectionID || server.ID == "" || server.ExternalID == "" {
			return errors.New("invalid synchronized server")
		}
		if err := repository.upsert(ctx, transaction, server); err != nil {
			return err
		}
	}
	result, err := transaction.ExecContext(ctx, `UPDATE provider_connections SET last_synced_at = ?, health_status = ?, last_error_code = '', last_error_message = '', updated_at = ? WHERE id = ? AND deleted_at IS NULL`, at, "healthy", at, snapshot.ConnectionID)
	if err != nil {
		return fmt.Errorf("update connection sync status: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return errors.New("provider connection not found")
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit inventory sync: %w", err)
	}
	return nil
}

func (repository *SQLRepository) upsert(ctx context.Context, transaction *sql.Tx, server Server) error {
	arguments := []any{server.ID, server.ConnectionID, server.ExternalID, server.Scope, server.Name, server.State,
		server.RemoteState, string(server.Spec), string(server.Addresses), string(server.Capabilities), server.PortalURL,
		repository.timeValue(server.LastSeenAt), repository.timeValue(server.LastStateCheckedAt), repository.timeValue(server.CreatedAt), repository.timeValue(server.UpdatedAt)}
	query := `INSERT INTO servers (id, connection_id, external_id, scope, name, normalized_state, remote_state, spec_json,
		addresses_json, capabilities_json, portal_url, last_seen_at, last_state_checked_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if repository.dialect == database.DialectMySQL {
		query += ` ON DUPLICATE KEY UPDATE name = VALUES(name), normalized_state = VALUES(normalized_state), remote_state = VALUES(remote_state),
			spec_json = VALUES(spec_json), addresses_json = VALUES(addresses_json), capabilities_json = VALUES(capabilities_json),
			portal_url = VALUES(portal_url), last_seen_at = VALUES(last_seen_at), last_state_checked_at = VALUES(last_state_checked_at),
			hidden_at = NULL, updated_at = VALUES(updated_at)`
	} else {
		query += ` ON CONFLICT(connection_id, scope, external_id) DO UPDATE SET name = excluded.name,
			normalized_state = excluded.normalized_state, remote_state = excluded.remote_state, spec_json = excluded.spec_json,
			addresses_json = excluded.addresses_json, capabilities_json = excluded.capabilities_json, portal_url = excluded.portal_url,
			last_seen_at = excluded.last_seen_at, last_state_checked_at = excluded.last_state_checked_at, hidden_at = NULL, updated_at = excluded.updated_at`
	}
	if _, err := transaction.ExecContext(ctx, query, arguments...); err != nil {
		return fmt.Errorf("upsert synchronized server: %w", err)
	}
	return nil
}

func (repository *SQLRepository) List(ctx context.Context, filter Filter) ([]Server, error) {
	query := `SELECT s.id, s.connection_id, s.external_id, s.scope, s.name, s.normalized_state, s.remote_state,
		s.spec_json, s.addresses_json, s.capabilities_json, s.portal_url, s.last_seen_at, s.last_state_checked_at, s.created_at, s.updated_at
		FROM servers s JOIN provider_connections c ON c.id = s.connection_id WHERE s.hidden_at IS NULL AND c.deleted_at IS NULL`
	arguments := make([]any, 0, 4)
	if filter.ConnectionID != "" {
		query += ` AND s.connection_id = ?`
		arguments = append(arguments, filter.ConnectionID)
	}
	if filter.ProviderType != "" {
		query += ` AND c.provider_type = ?`
		arguments = append(arguments, filter.ProviderType)
	}
	if filter.State != "" {
		query += ` AND s.normalized_state = ?`
		arguments = append(arguments, filter.State)
	}
	if strings.TrimSpace(filter.Query) != "" {
		query += ` AND LOWER(s.name) LIKE ?`
		arguments = append(arguments, "%"+strings.ToLower(strings.TrimSpace(filter.Query))+"%")
	}
	query += ` ORDER BY s.name, s.id`
	rows, err := repository.db.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list servers: %w", err)
	}
	defer rows.Close()
	servers := make([]Server, 0)
	for rows.Next() {
		server, err := scanServer(rows.Scan)
		if err != nil {
			return nil, err
		}
		servers = append(servers, server)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list servers: %w", err)
	}
	return servers, nil
}

func (repository *SQLRepository) FindByID(ctx context.Context, id string) (Server, error) {
	row := repository.db.QueryRowContext(ctx, `SELECT s.id, s.connection_id, s.external_id, s.scope, s.name, s.normalized_state,
		s.remote_state, s.spec_json, s.addresses_json, s.capabilities_json, s.portal_url, s.last_seen_at,
		s.last_state_checked_at, s.created_at, s.updated_at FROM servers s JOIN provider_connections c ON c.id = s.connection_id
		WHERE s.id = ? AND s.hidden_at IS NULL AND c.deleted_at IS NULL`, id)
	server, err := scanServer(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return Server{}, ErrNotFound
	}
	if err != nil {
		return Server{}, fmt.Errorf("find server: %w", err)
	}
	return server, nil
}

type scanFunc func(...any) error

func scanServer(scan scanFunc) (Server, error) {
	var server Server
	var spec, addresses, capabilities, seen, checked, created, updated string
	var portal sql.NullString
	if err := scan(&server.ID, &server.ConnectionID, &server.ExternalID, &server.Scope, &server.Name, &server.State,
		&server.RemoteState, &spec, &addresses, &capabilities, &portal, &seen, &checked, &created, &updated); err != nil {
		return Server{}, err
	}
	server.Spec, server.Addresses, server.Capabilities = []byte(spec), []byte(addresses), []byte(capabilities)
	if portal.Valid {
		server.PortalURL = &portal.String
	}
	values := []struct {
		target *time.Time
		raw    string
	}{{&server.LastSeenAt, seen}, {&server.LastStateCheckedAt, checked}, {&server.CreatedAt, created}, {&server.UpdatedAt, updated}}
	for _, value := range values {
		parsed, err := parseTime(value.raw)
		if err != nil {
			return Server{}, err
		}
		*value.target = parsed
	}
	return server, nil
}

func (repository *SQLRepository) timeValue(value time.Time) any {
	if repository.dialect == database.DialectMySQL {
		return value.UTC()
	}
	return value.UTC().Format(time.RFC3339Nano)
}
func parseTime(value string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999", "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, errors.New("invalid database time")
}
