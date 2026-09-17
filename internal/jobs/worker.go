package jobs

import (
	"context"
	"errors"
	"strings"
	"time"
)

type Handler interface {
	Handle(context.Context, Job) error
}
type HandlerFunc func(context.Context, Job) error

func (function HandlerFunc) Handle(ctx context.Context, job Job) error { return function(ctx, job) }

type WorkerOptions struct {
	Owner         string
	Now           func() time.Time
	PollInterval  time.Duration
	LeaseDuration time.Duration
	RetryDelay    func(int) time.Duration
}

type Worker struct {
	repository Repository
	handler    Handler
	options    WorkerOptions
}

func NewWorker(repository Repository, handler Handler, options WorkerOptions) *Worker {
	if options.Owner == "" {
		options.Owner = "worker"
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	if options.PollInterval <= 0 {
		options.PollInterval = time.Second
	}
	if options.LeaseDuration <= 0 {
		options.LeaseDuration = 30 * time.Second
	}
	if options.RetryDelay == nil {
		options.RetryDelay = func(attempt int) time.Duration {
			delay := time.Second << min(attempt-1, 8)
			return min(delay, 5*time.Minute)
		}
	}
	return &Worker{repository: repository, handler: handler, options: options}
}

func (worker *Worker) Run(ctx context.Context) error {
	for {
		err := worker.RunOnce(ctx)
		if err == nil {
			continue
		}
		if !errors.Is(err, ErrNoJob) {
			return err
		}
		timer := time.NewTimer(worker.options.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func (worker *Worker) RunOnce(ctx context.Context) error {
	now := worker.options.Now().UTC()
	lease, err := worker.repository.LeaseNext(ctx, worker.options.Owner, now, worker.options.LeaseDuration)
	if err != nil {
		return err
	}
	handleErr := worker.handler.Handle(ctx, lease.Job)
	if handleErr == nil {
		return worker.repository.Complete(ctx, lease.Job.ID, lease.Owner, worker.options.Now().UTC())
	}
	message := safeErrorMessage(handleErr)
	var retryable interface{ RetryableError() bool }
	if errors.As(handleErr, &retryable) && retryable.RetryableError() {
		availableAt := worker.options.Now().UTC().Add(worker.options.RetryDelay(lease.Job.Attempts))
		return worker.repository.Retry(ctx, lease.Job.ID, lease.Owner, availableAt, message)
	}
	return worker.repository.Fail(ctx, lease.Job.ID, lease.Owner, worker.options.Now().UTC(), message)
}

func safeErrorMessage(err error) string {
	message := err.Error()
	var safe interface{ SafeMessage() string }
	if errors.As(err, &safe) {
		message = safe.SafeMessage()
	}
	message = strings.TrimSpace(message)
	if len(message) > 1024 {
		message = message[:1024]
	}
	return message
}
