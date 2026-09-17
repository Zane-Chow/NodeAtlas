package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"controlpanel/internal/database"
)

const metadataColumns = `id, filename, size_bytes, sha256, format_version, kind, status, manifest_json, created_at`

type SQLRepository struct {
	db      *sql.DB
	dialect database.Dialect
}

func NewSQLRepository(db *sql.DB, dialect database.Dialect) *SQLRepository {
	return &SQLRepository{db: db, dialect: dialect}
}

func (repository *SQLRepository) Create(ctx context.Context, metadata Metadata) error {
	_, err := repository.db.ExecContext(ctx, `INSERT INTO backups
		(id, filename, size_bytes, sha256, format_version, kind, status, manifest_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, metadata.ID, metadata.Filename, metadata.SizeBytes, metadata.SHA256,
		metadata.FormatVersion, metadata.Kind, metadata.Status, []byte(metadata.Manifest), repository.timeValue(metadata.CreatedAt))
	if err != nil {
		return fmt.Errorf("create backup metadata: %w", err)
	}
	return nil
}

func (repository *SQLRepository) FindByID(ctx context.Context, id string) (Metadata, error) {
	return scanMetadata(repository.db.QueryRowContext(ctx, `SELECT `+metadataColumns+` FROM backups WHERE id = ?`, id).Scan)
}

func (repository *SQLRepository) List(ctx context.Context) ([]Metadata, error) {
	rows, err := repository.db.QueryContext(ctx, `SELECT `+metadataColumns+` FROM backups ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Metadata, 0)
	for rows.Next() {
		item, scanErr := scanMetadata(rows.Scan)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

type scanFunc func(...any) error

func scanMetadata(scan scanFunc) (Metadata, error) {
	var metadata Metadata
	var manifest []byte
	var createdAt string
	if err := scan(&metadata.ID, &metadata.Filename, &metadata.SizeBytes, &metadata.SHA256, &metadata.FormatVersion,
		&metadata.Kind, &metadata.Status, &manifest, &createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Metadata{}, ErrNotFound
		}
		return Metadata{}, err
	}
	metadata.Manifest = manifest
	var err error
	metadata.CreatedAt, err = parseTime(createdAt)
	return metadata, err
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
