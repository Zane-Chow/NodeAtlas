package backup

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"controlpanel/internal/database"
)

type columnKind int

const (
	kindText columnKind = iota
	kindJSON
	kindBinary
	kindInteger
	kindBoolean
	kindTime
)

type columnSchema struct {
	name string
	kind columnKind
}
type tableSchema struct {
	name    string
	columns []columnSchema
}

var logicalSchema = []tableSchema{
	{name: "users", columns: columns(kindInteger, "singleton_key", kindText, "id", kindText, "username", kindText, "normalized_username", kindText, "password_hash", kindTime, "initialized_at", kindTime, "created_at", kindTime, "updated_at", kindTime, "last_login_at")},
	{name: "sessions", columns: columns(kindBinary, "token_hash", kindBinary, "csrf_hash", kindText, "user_id", kindTime, "expires_at", kindTime, "absolute_expires_at", kindTime, "last_seen_at", kindTime, "created_at")},
	{name: "provider_connections", columns: columns(kindText, "id", kindText, "name", kindText, "provider_type", kindText, "endpoint", kindJSON, "settings_json", kindBinary, "credentials_ciphertext", kindBinary, "credentials_nonce", kindInteger, "credentials_key_version", kindBoolean, "enabled", kindText, "health_status", kindTime, "last_tested_at", kindTime, "last_synced_at", kindText, "last_error_code", kindText, "last_error_message", kindTime, "created_at", kindTime, "updated_at", kindTime, "deleted_at")},
	{name: "servers", columns: columns(kindText, "id", kindText, "connection_id", kindText, "external_id", kindText, "scope", kindText, "name", kindText, "normalized_state", kindText, "remote_state", kindJSON, "spec_json", kindJSON, "addresses_json", kindJSON, "capabilities_json", kindText, "portal_url", kindTime, "last_seen_at", kindTime, "last_state_checked_at", kindTime, "hidden_at", kindTime, "created_at", kindTime, "updated_at")},
	{name: "jobs", columns: columns(kindText, "id", kindText, "kind", kindJSON, "payload_json", kindText, "status", kindInteger, "attempts", kindInteger, "max_attempts", kindTime, "available_at", kindText, "lease_owner", kindTime, "lease_expires_at", kindText, "last_error", kindTime, "created_at", kindTime, "updated_at")},
	{name: "operations", columns: columns(kindText, "id", kindText, "server_id", kindText, "connection_id", kindText, "action", kindText, "status", kindText, "idempotency_key", kindText, "active_marker", kindText, "provider_request_id", kindText, "error_code", kindText, "error_message", kindTime, "queued_at", kindTime, "started_at", kindTime, "finished_at", kindTime, "updated_at")},
	{name: "audit_logs", columns: columns(kindText, "id", kindText, "event_type", kindText, "target_type", kindText, "target_id", kindText, "request_id", kindText, "source_ip", kindJSON, "metadata_json", kindTime, "created_at")},
	{name: "console_sessions", columns: columns(kindText, "id", kindText, "server_id", kindText, "mode", kindBinary, "ticket_hash", kindTime, "expires_at", kindTime, "opened_at", kindTime, "closed_at", kindText, "result", kindTime, "created_at")},
	{name: "settings", columns: columns(kindText, "setting_key", kindJSON, "value_json", kindTime, "updated_at")},
}

type SnapshotOptions struct {
	ApplicationVersion string
	Now                func() time.Time
}
type Snapshotter struct {
	db      *sql.DB
	dialect database.Dialect
	options SnapshotOptions
}

func NewSnapshotter(db *sql.DB, dialect database.Dialect, options SnapshotOptions) *Snapshotter {
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Snapshotter{db: db, dialect: dialect, options: options}
}

func (snapshotter *Snapshotter) Export(ctx context.Context) (Archive, error) {
	archive := Archive{FormatVersion: CurrentFormatVersion, ApplicationVersion: snapshotter.options.ApplicationVersion, CreatedAt: snapshotter.options.Now().UTC(), SourceDialect: snapshotter.dialect, Manifest: make(map[string]int), Tables: make(map[string]TableData)}
	transaction, err := snapshotter.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Archive{}, err
	}
	defer func() { _ = transaction.Rollback() }()
	for _, schema := range logicalSchema {
		table, err := exportTable(ctx, transaction, schema)
		if err != nil {
			return Archive{}, err
		}
		archive.Tables[schema.name] = table
		archive.Manifest[schema.name] = len(table.Rows)
	}
	if err := transaction.Commit(); err != nil {
		return Archive{}, err
	}
	return archive, nil
}

func (snapshotter *Snapshotter) Restore(ctx context.Context, archive Archive) error {
	if err := validateLogicalArchive(archive); err != nil {
		return err
	}
	transaction, err := snapshotter.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = transaction.Rollback() }()
	for _, name := range []string{"console_sessions", "operations", "servers", "sessions", "jobs", "audit_logs", "settings", "provider_connections", "users"} {
		if _, err := transaction.ExecContext(ctx, `DELETE FROM `+name); err != nil {
			return fmt.Errorf("clear %s: %w", name, err)
		}
	}
	for _, schema := range logicalSchema {
		if err := restoreTable(ctx, transaction, snapshotter.dialect, schema, archive.Tables[schema.name]); err != nil {
			return err
		}
	}
	return transaction.Commit()
}

func exportTable(ctx context.Context, transaction *sql.Tx, schema tableSchema) (TableData, error) {
	names := columnNames(schema)
	rows, err := transaction.QueryContext(ctx, `SELECT `+strings.Join(names, ", ")+` FROM `+schema.name)
	if err != nil {
		return TableData{}, fmt.Errorf("export %s: %w", schema.name, err)
	}
	defer rows.Close()
	table := TableData{Columns: names, Rows: make([][]*string, 0)}
	for rows.Next() {
		values := make([]any, len(schema.columns))
		destinations := make([]any, len(values))
		for index := range values {
			destinations[index] = &values[index]
		}
		if err := rows.Scan(destinations...); err != nil {
			return TableData{}, err
		}
		row := make([]*string, len(values))
		for index, value := range values {
			normalized, err := normalizeCell(schema.columns[index].kind, value)
			if err != nil {
				return TableData{}, fmt.Errorf("export %s.%s: %w", schema.name, schema.columns[index].name, err)
			}
			row[index] = normalized
		}
		table.Rows = append(table.Rows, row)
	}
	return table, rows.Err()
}

func restoreTable(ctx context.Context, transaction *sql.Tx, dialect database.Dialect, schema tableSchema, table TableData) error {
	if len(table.Rows) == 0 {
		return nil
	}
	names := columnNames(schema)
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(names)), ",")
	query := `INSERT INTO ` + schema.name + ` (` + strings.Join(names, ", ") + `) VALUES (` + placeholders + `)`
	for _, row := range table.Rows {
		values := make([]any, len(row))
		for index, value := range row {
			restored, err := restoreCell(schema.columns[index].kind, value, dialect)
			if err != nil {
				return fmt.Errorf("restore %s.%s: %w", schema.name, schema.columns[index].name, err)
			}
			values[index] = restored
		}
		if _, err := transaction.ExecContext(ctx, query, values...); err != nil {
			return fmt.Errorf("restore %s: %w", schema.name, err)
		}
	}
	return nil
}

func validateLogicalArchive(archive Archive) error {
	if err := archive.validateEnvelopeManifest(); err != nil {
		return err
	}
	if len(archive.Tables) != len(logicalSchema) {
		return ErrInvalidManifest
	}
	for _, schema := range logicalSchema {
		table, ok := archive.Tables[schema.name]
		if !ok || archive.Manifest[schema.name] != len(table.Rows) || !reflect.DeepEqual(table.Columns, columnNames(schema)) {
			return ErrInvalidManifest
		}
		for _, row := range table.Rows {
			if len(row) != len(schema.columns) {
				return ErrInvalidManifest
			}
			for index, value := range row {
				if _, err := restoreCell(schema.columns[index].kind, value, database.DialectSQLite); err != nil {
					return ErrInvalidManifest
				}
			}
		}
	}
	return nil
}

func normalizeCell(kind columnKind, value any) (*string, error) {
	if value == nil {
		return nil, nil
	}
	var result string
	switch kind {
	case kindBinary:
		bytes, ok := value.([]byte)
		if !ok {
			return nil, fmt.Errorf("expected binary")
		}
		result = base64.StdEncoding.EncodeToString(bytes)
	case kindInteger, kindBoolean:
		result = scalarString(value)
		if kind == kindInteger {
			if _, err := strconv.ParseInt(result, 10, 64); err != nil {
				return nil, err
			}
		}
		if kind == kindBoolean && result != "0" && result != "1" && result != "true" && result != "false" {
			return nil, fmt.Errorf("invalid boolean")
		}
	case kindTime:
		parsed, err := valueTime(value)
		if err != nil {
			return nil, err
		}
		result = parsed.UTC().Format(time.RFC3339Nano)
	case kindJSON:
		result = scalarString(value)
		if !json.Valid([]byte(result)) {
			return nil, fmt.Errorf("invalid JSON")
		}
	default:
		result = scalarString(value)
	}
	return &result, nil
}

func restoreCell(kind columnKind, value *string, dialect database.Dialect) (any, error) {
	if value == nil {
		return nil, nil
	}
	switch kind {
	case kindBinary:
		return base64.StdEncoding.DecodeString(*value)
	case kindInteger:
		return strconv.ParseInt(*value, 10, 64)
	case kindBoolean:
		parsed, err := strconv.ParseBool(*value)
		if err == nil {
			return parsed, nil
		}
		integer, err := strconv.ParseInt(*value, 10, 64)
		if err != nil || (integer != 0 && integer != 1) {
			return nil, fmt.Errorf("invalid boolean")
		}
		return integer == 1, nil
	case kindTime:
		parsed, err := time.Parse(time.RFC3339Nano, *value)
		if err != nil {
			return nil, err
		}
		if dialect == database.DialectMySQL {
			return parsed.UTC(), nil
		}
		return parsed.UTC().Format(time.RFC3339Nano), nil
	case kindJSON:
		if !json.Valid([]byte(*value)) {
			return nil, fmt.Errorf("invalid JSON")
		}
		return *value, nil
	default:
		return *value, nil
	}
}

func valueTime(value any) (time.Time, error) {
	if parsed, ok := value.(time.Time); ok {
		return parsed, nil
	}
	raw := scalarString(value)
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999", "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid timestamp")
}

func scalarString(value any) string {
	switch typed := value.(type) {
	case []byte:
		return string(typed)
	case string:
		return typed
	case int64:
		return strconv.FormatInt(typed, 10)
	case bool:
		return strconv.FormatBool(typed)
	default:
		return fmt.Sprint(value)
	}
}

func columns(values ...any) []columnSchema {
	result := make([]columnSchema, 0, len(values)/2)
	for index := 0; index < len(values); index += 2 {
		result = append(result, columnSchema{kind: values[index].(columnKind), name: values[index+1].(string)})
	}
	return result
}

func columnNames(schema tableSchema) []string {
	result := make([]string, len(schema.columns))
	for index, column := range schema.columns {
		result[index] = column.name
	}
	return result
}
