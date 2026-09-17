package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

func Migrate(ctx context.Context, db *sql.DB, dialect Dialect) error {
	if dialect != DialectSQLite && dialect != DialectMySQL {
		return errors.New("unsupported migration dialect")
	}
	if _, err := db.ExecContext(ctx, schemaMigrationsSQL(dialect)); err != nil {
		return fmt.Errorf("create migration table: %w", err)
	}

	for _, item := range migrations {
		var applied int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version = ?", item.Version).Scan(&applied); err != nil {
			return fmt.Errorf("read migration version %d: %w", item.Version, err)
		}
		if applied > 0 {
			continue
		}
		statements := item.SQLite
		if dialect == DialectMySQL {
			statements = item.MySQL
		}
		if err := applyMigration(ctx, db, item.Version, statements); err != nil {
			return err
		}
	}
	return nil
}

func applyMigration(ctx context.Context, db *sql.DB, version int, statements []string) error {
	transaction, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %d: %w", version, err)
	}
	defer func() { _ = transaction.Rollback() }()

	for _, statement := range statements {
		if _, err := transaction.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply migration %d: %w", version, err)
		}
	}
	if _, err := transaction.ExecContext(ctx, "INSERT INTO schema_migrations (version) VALUES (?)", version); err != nil {
		return fmt.Errorf("record migration %d: %w", version, err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit migration %d: %w", version, err)
	}
	return nil
}

func schemaMigrationsSQL(dialect Dialect) string {
	if dialect == DialectMySQL {
		return `CREATE TABLE IF NOT EXISTS schema_migrations (
			version BIGINT NOT NULL PRIMARY KEY,
			applied_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
		) ENGINE=InnoDB`
	}
	return `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`
}
