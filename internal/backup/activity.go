package backup

import (
	"context"
	"database/sql"
)

type SQLActivityChecker struct{ db *sql.DB }

func NewSQLActivityChecker(db *sql.DB) *SQLActivityChecker { return &SQLActivityChecker{db: db} }

func (checker *SQLActivityChecker) HasActiveWork(ctx context.Context) (bool, error) {
	var count int
	err := checker.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM operations WHERE status IN ('queued', 'running', 'verifying')) +
		(SELECT COUNT(*) FROM jobs WHERE status IN ('queued', 'running'))`).Scan(&count)
	return count > 0, err
}
