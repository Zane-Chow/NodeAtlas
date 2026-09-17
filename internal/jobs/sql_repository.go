package jobs

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

func (repository *SQLRepository) Enqueue(ctx context.Context, job Job) error {
	_, err := repository.db.ExecContext(ctx, `INSERT INTO jobs (id, kind, payload_json, status, attempts, max_attempts,
		available_at, last_error, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, job.ID, job.Kind,
		string(job.Payload), job.Status, job.Attempts, job.MaxAttempts, repository.timeValue(job.AvailableAt), job.LastError,
		repository.timeValue(job.CreatedAt), repository.timeValue(job.UpdatedAt))
	if err != nil {
		return fmt.Errorf("enqueue job: %w", err)
	}
	return nil
}

func (repository *SQLRepository) LeaseNext(ctx context.Context, owner string, now time.Time, duration time.Duration) (Lease, error) {
	transaction, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return Lease{}, err
	}
	defer func() { _ = transaction.Rollback() }()
	query := `SELECT id, kind, payload_json, status, attempts, max_attempts, available_at, lease_owner,
		lease_expires_at, last_error, created_at, updated_at FROM jobs
		WHERE (status = ? AND available_at <= ?) OR (status = ? AND lease_expires_at <= ?)
		ORDER BY available_at, created_at LIMIT 1`
	if repository.dialect == database.DialectMySQL {
		query += ` FOR UPDATE SKIP LOCKED`
	}
	job, err := scanJob(transaction.QueryRowContext(ctx, query, StatusQueued, repository.timeValue(now), StatusRunning, repository.timeValue(now)).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return Lease{}, ErrNoJob
	}
	if err != nil {
		return Lease{}, fmt.Errorf("select job lease: %w", err)
	}
	expires := now.Add(duration).UTC()
	result, err := transaction.ExecContext(ctx, `UPDATE jobs SET status = ?, attempts = attempts + 1, lease_owner = ?, lease_expires_at = ?, updated_at = ? WHERE id = ?`,
		StatusRunning, owner, repository.timeValue(expires), repository.timeValue(now), job.ID)
	if err != nil {
		return Lease{}, fmt.Errorf("claim job lease: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return Lease{}, ErrLeaseLost
	}
	if err := transaction.Commit(); err != nil {
		return Lease{}, err
	}
	job.Status, job.Attempts, job.LeaseOwner, job.LeaseExpiresAt, job.UpdatedAt = StatusRunning, job.Attempts+1, owner, &expires, now.UTC()
	return Lease{Job: job, Owner: owner, ExpiresAt: expires}, nil
}

func (repository *SQLRepository) Retry(ctx context.Context, id, owner string, availableAt time.Time, message string) error {
	transaction, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = transaction.Rollback() }()
	var attempts, maxAttempts int
	if err := transaction.QueryRowContext(ctx, `SELECT attempts, max_attempts FROM jobs WHERE id = ? AND status = ? AND lease_owner = ?`, id, StatusRunning, owner).Scan(&attempts, &maxAttempts); errors.Is(err, sql.ErrNoRows) {
		return ErrLeaseLost
	} else if err != nil {
		return err
	}
	status := StatusQueued
	if attempts >= maxAttempts {
		status = StatusFailed
	}
	result, err := transaction.ExecContext(ctx, `UPDATE jobs SET status = ?, available_at = ?, lease_owner = NULL,
		lease_expires_at = NULL, last_error = ?, updated_at = ? WHERE id = ? AND status = ? AND lease_owner = ?`,
		status, repository.timeValue(availableAt), message, repository.timeValue(availableAt), id, StatusRunning, owner)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrLeaseLost
	}
	return transaction.Commit()
}

func (repository *SQLRepository) Complete(ctx context.Context, id, owner string, at time.Time) error {
	return repository.finish(ctx, id, owner, StatusSucceeded, at, "")
}
func (repository *SQLRepository) Fail(ctx context.Context, id, owner string, at time.Time, message string) error {
	return repository.finish(ctx, id, owner, StatusFailed, at, message)
}
func (repository *SQLRepository) finish(ctx context.Context, id, owner string, status Status, at time.Time, message string) error {
	result, err := repository.db.ExecContext(ctx, `UPDATE jobs SET status = ?, lease_owner = NULL, lease_expires_at = NULL,
		last_error = CASE WHEN ? = '' THEN last_error ELSE ? END, updated_at = ? WHERE id = ? AND status = ? AND lease_owner = ?`,
		status, message, message, repository.timeValue(at), id, StatusRunning, owner)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrLeaseLost
	}
	return nil
}
func (repository *SQLRepository) FindByID(ctx context.Context, id string) (Job, error) {
	job, err := scanJob(repository.db.QueryRowContext(ctx, `SELECT id, kind, payload_json, status, attempts, max_attempts,
		available_at, lease_owner, lease_expires_at, last_error, created_at, updated_at FROM jobs WHERE id = ?`, id).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	return job, err
}

type scanFunc func(...any) error

func scanJob(scan scanFunc) (Job, error) {
	var job Job
	var payload, available, created, updated string
	var owner, expires sql.NullString
	if err := scan(&job.ID, &job.Kind, &payload, &job.Status, &job.Attempts, &job.MaxAttempts, &available, &owner, &expires, &job.LastError, &created, &updated); err != nil {
		return Job{}, err
	}
	job.Payload = []byte(payload)
	job.LeaseOwner = owner.String
	var err error
	if job.AvailableAt, err = parseTime(available); err != nil {
		return Job{}, err
	}
	if job.CreatedAt, err = parseTime(created); err != nil {
		return Job{}, err
	}
	if job.UpdatedAt, err = parseTime(updated); err != nil {
		return Job{}, err
	}
	if expires.Valid {
		parsed, err := parseTime(expires.String)
		if err != nil {
			return Job{}, err
		}
		job.LeaseExpiresAt = &parsed
	}
	return job, nil
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
