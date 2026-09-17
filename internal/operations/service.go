package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"controlpanel/internal/audit"
	"controlpanel/internal/inventory"
	"controlpanel/internal/providers"
	"github.com/google/uuid"
)

var (
	ErrInvalidAction         = errors.New("invalid power action")
	ErrStateConflict         = errors.New("server state conflicts with action")
	ErrCapabilityUnavailable = errors.New("server capability is unavailable")
)

type InventoryRepository interface {
	FindByID(context.Context, string) (inventory.Server, error)
}

type PowerQueue interface {
	EnqueuePowerOperation(context.Context, string) error
}

type AuditAppender interface {
	Append(context.Context, audit.Entry) error
}

type Request struct {
	ServerID       string
	Action         Action
	IdempotencyKey string
	RequestID      string
	SourceIP       string
}

type ServiceOptions struct {
	Now               func() time.Time
	NewID             func() string
	NewIdempotencyKey func() string
	NewAuditID        func() string
}

type Service struct {
	repository Repository
	inventory  InventoryRepository
	queue      PowerQueue
	audit      AuditAppender
	options    ServiceOptions
}

func NewService(repository Repository, inventoryRepository InventoryRepository, queue PowerQueue, auditLog AuditAppender, options ServiceOptions) *Service {
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	if options.NewID == nil {
		options.NewID = uuid.NewString
	}
	if options.NewIdempotencyKey == nil {
		options.NewIdempotencyKey = uuid.NewString
	}
	if options.NewAuditID == nil {
		options.NewAuditID = uuid.NewString
	}
	return &Service{repository: repository, inventory: inventoryRepository, queue: queue, audit: auditLog, options: options}
}

func (service *Service) Request(ctx context.Context, request Request) (Operation, bool, error) {
	request.ServerID = strings.TrimSpace(request.ServerID)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	if !validAction(request.Action) {
		return Operation{}, false, ErrInvalidAction
	}
	if request.IdempotencyKey == "" {
		request.IdempotencyKey = service.options.NewIdempotencyKey()
	}
	if existing, err := service.repository.FindByIdempotencyKey(ctx, request.IdempotencyKey); err == nil {
		return existing, false, nil
	} else if !errors.Is(err, ErrNotFound) {
		return Operation{}, false, err
	}
	server, err := service.inventory.FindByID(ctx, request.ServerID)
	if err != nil {
		return Operation{}, false, err
	}
	var capabilities providers.Capabilities
	if err := json.Unmarshal(server.Capabilities, &capabilities); err != nil {
		return Operation{}, false, fmt.Errorf("decode server capabilities: %w", err)
	}
	if !stateAllows(server.State, request.Action) {
		return Operation{}, false, ErrStateConflict
	}
	if !capabilityFor(capabilities, request.Action).Available {
		return Operation{}, false, ErrCapabilityUnavailable
	}
	now := service.options.Now().UTC()
	operation, created, err := service.repository.CreateQueued(ctx, Operation{
		ID: service.options.NewID(), ServerID: server.ID, ConnectionID: server.ConnectionID, Action: request.Action,
		Status: StatusQueued, IdempotencyKey: request.IdempotencyKey, QueuedAt: now, UpdatedAt: now,
	})
	if err != nil || !created {
		return operation, created, err
	}
	if err := service.queue.EnqueuePowerOperation(ctx, operation.ID); err != nil {
		_, _ = service.repository.Transition(ctx, operation.ID, StatusFailed, Transition{At: now, ErrorCode: "queue_failed", ErrorMessage: "Unable to queue power operation"})
		return Operation{}, false, fmt.Errorf("queue power operation: %w", err)
	}
	metadata, _ := json.Marshal(map[string]string{"action": string(operation.Action), "status": string(operation.Status)})
	if err := service.audit.Append(ctx, audit.Entry{
		ID: service.options.NewAuditID(), EventType: "power_operation_queued", TargetType: "operation", TargetID: operation.ID,
		RequestID: request.RequestID, SourceIP: request.SourceIP, Metadata: metadata, CreatedAt: now,
	}); err != nil {
		_, _ = service.repository.Transition(ctx, operation.ID, StatusFailed, Transition{At: now, ErrorCode: "audit_failed", ErrorMessage: "Unable to record operation audit event"})
		return Operation{}, false, fmt.Errorf("append operation audit: %w", err)
	}
	return operation, true, nil
}

func validAction(action Action) bool {
	return action == ActionStart || action == ActionStop || action == ActionReboot
}

func stateAllows(state inventory.State, action Action) bool {
	switch action {
	case ActionStart:
		return state == inventory.StateStopped || state == inventory.StateSuspended
	case ActionStop, ActionReboot:
		return state == inventory.StateRunning
	default:
		return false
	}
}

func capabilityFor(capabilities providers.Capabilities, action Action) providers.Capability {
	switch action {
	case ActionStart:
		return capabilities.CanStart
	case ActionStop:
		return capabilities.CanStop
	case ActionReboot:
		return capabilities.CanReboot
	default:
		return providers.Capability{}
	}
}
