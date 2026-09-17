package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

type QueueOptions struct {
	Now         func() time.Time
	NewID       func() string
	MaxAttempts int
}

type Queue struct {
	repository  Repository
	now         func() time.Time
	newID       func() string
	maxAttempts int
}

func NewQueue(repository Repository, options QueueOptions) *Queue {
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	if options.NewID == nil {
		options.NewID = uuid.NewString
	}
	if options.MaxAttempts < 1 {
		options.MaxAttempts = 3
	}
	return &Queue{repository: repository, now: options.Now, newID: options.NewID, maxAttempts: options.MaxAttempts}
}

func (queue *Queue) EnqueueConnectionSync(ctx context.Context, connectionID string) error {
	if connectionID == "" {
		return errors.New("connection ID is required")
	}
	payload, err := json.Marshal(map[string]string{"connection_id": connectionID})
	if err != nil {
		return err
	}
	now := queue.now().UTC()
	return queue.repository.Enqueue(ctx, Job{
		ID: queue.newID(), Kind: KindSyncConnection, Payload: payload, Status: StatusQueued,
		MaxAttempts: queue.maxAttempts, AvailableAt: now, CreatedAt: now, UpdatedAt: now,
	})
}
