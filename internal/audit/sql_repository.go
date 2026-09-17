package audit

import (
	"context"
	"database/sql"
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

func (repository *SQLRepository) Append(ctx context.Context, entry Entry) error {
	_, err := repository.db.ExecContext(ctx, `INSERT INTO audit_logs
		(id, event_type, target_type, target_id, request_id, source_ip, metadata_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, entry.ID, entry.EventType, entry.TargetType, entry.TargetID,
		entry.RequestID, entry.SourceIP, string(entry.Metadata), repository.timeValue(entry.CreatedAt))
	if err != nil {
		return fmt.Errorf("append audit entry: %w", err)
	}
	return nil
}

func (repository *SQLRepository) List(ctx context.Context, filter Filter) ([]Entry, error) {
	conditions := make([]string, 0, 3)
	arguments := make([]any, 0, 3)
	if filter.EventType != "" {
		conditions, arguments = append(conditions, "event_type = ?"), append(arguments, filter.EventType)
	}
	if filter.TargetType != "" {
		conditions, arguments = append(conditions, "target_type = ?"), append(arguments, filter.TargetType)
	}
	if filter.TargetID != "" {
		conditions, arguments = append(conditions, "target_id = ?"), append(arguments, filter.TargetID)
	}
	query := `SELECT id, event_type, target_type, target_id, request_id, source_ip, metadata_json, created_at FROM audit_logs`
	if len(conditions) > 0 {
		query += ` WHERE ` + strings.Join(conditions, ` AND `)
	}
	query += ` ORDER BY created_at DESC, id DESC`
	rows, err := repository.db.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list audit entries: %w", err)
	}
	defer rows.Close()
	entries := make([]Entry, 0)
	for rows.Next() {
		var entry Entry
		var metadata, created string
		if err := rows.Scan(&entry.ID, &entry.EventType, &entry.TargetType, &entry.TargetID, &entry.RequestID,
			&entry.SourceIP, &metadata, &created); err != nil {
			return nil, err
		}
		entry.Metadata = []byte(metadata)
		entry.CreatedAt, err = parseAuditTime(created)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func (repository *SQLRepository) timeValue(value time.Time) any {
	if repository.dialect == database.DialectMySQL {
		return value.UTC()
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func parseAuditTime(value string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999", "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid database time")
}
