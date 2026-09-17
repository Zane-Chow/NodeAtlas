package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"controlpanel/internal/audit"
	"controlpanel/internal/connections"
	"controlpanel/internal/events"
	"controlpanel/internal/inventory"
	"controlpanel/internal/jobs"
	"controlpanel/internal/providers"
	"controlpanel/internal/secrets"
	"github.com/google/uuid"
)

type ExecutionInventory interface {
	FindByID(context.Context, string) (inventory.Server, error)
	UpdateRemote(context.Context, string, providers.RemoteServer, time.Time) error
}

type ExecutionConnections interface {
	FindByID(context.Context, string) (connections.Connection, connections.CredentialRecord, error)
}

type ExecutorOptions struct {
	Now                 func() time.Time
	PollInterval        time.Duration
	VerificationTimeout time.Duration
	NewAuditID          func() string
	Publisher           events.Publisher
}

type Executor struct {
	operations  Repository
	inventory   ExecutionInventory
	connections ExecutionConnections
	audit       AuditAppender
	cipher      *secrets.CredentialCipher
	registry    *providers.Registry
	options     ExecutorOptions
}

func NewExecutor(operationRepository Repository, inventoryRepository ExecutionInventory, connectionRepository ExecutionConnections, auditLog AuditAppender, cipher *secrets.CredentialCipher, registry *providers.Registry, options ExecutorOptions) *Executor {
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	if options.PollInterval <= 0 {
		options.PollInterval = 2 * time.Second
	}
	if options.VerificationTimeout <= 0 {
		options.VerificationTimeout = 2 * time.Minute
	}
	if options.NewAuditID == nil {
		options.NewAuditID = uuid.NewString
	}
	return &Executor{operations: operationRepository, inventory: inventoryRepository, connections: connectionRepository, audit: auditLog, cipher: cipher, registry: registry, options: options}
}

func (executor *Executor) Execute(ctx context.Context, job jobs.Job) error {
	if job.Kind != jobs.KindPowerOperation {
		return errors.New("unsupported power operation job")
	}
	var payload struct {
		OperationID string `json:"operation_id"`
	}
	if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.OperationID == "" {
		return errors.New("invalid power operation job payload")
	}
	operation, err := executor.operations.FindByID(ctx, payload.OperationID)
	if err != nil {
		return err
	}
	if operation.Status.terminal() {
		return nil
	}
	if operation.Status == StatusQueued {
		operation, err = executor.operations.Transition(ctx, operation.ID, StatusRunning, Transition{At: executor.options.Now()})
		if err != nil {
			return err
		}
		executor.publishOperation(operation)
	}
	server, err := executor.inventory.FindByID(ctx, operation.ServerID)
	if err != nil {
		return executor.finishError(ctx, operation, job, err)
	}
	provider, err := executor.provider(ctx, operation.ConnectionID)
	if err != nil {
		return executor.finishError(ctx, operation, job, err)
	}
	if _, err := provider.ValidateConnection(ctx); err != nil {
		return executor.finishError(ctx, operation, job, err)
	}
	ref := providers.ServerRef{ExternalID: server.ExternalID, Scope: server.Scope}
	remote, err := provider.GetServer(ctx, ref)
	if err != nil {
		return executor.finishError(ctx, operation, job, err)
	}
	if actionAlreadySatisfied(operation.Action, remote.State) {
		return executor.succeed(ctx, operation, remote)
	}
	if operation.Status != StatusVerifying {
		receipt, err := executeProviderAction(ctx, provider, ref, operation.Action)
		if err != nil {
			return executor.finishError(ctx, operation, job, err)
		}
		operation, err = executor.operations.Transition(ctx, operation.ID, StatusVerifying, Transition{
			At: executor.options.Now(), ProviderRequestID: receipt.RequestID,
		})
		if err != nil {
			return err
		}
		executor.publishOperation(operation)
	}
	deadline := time.Now().Add(executor.options.VerificationTimeout)
	for {
		remote, err = provider.GetServer(ctx, ref)
		if err != nil {
			return executor.finishError(ctx, operation, job, err)
		}
		if verificationTargetReached(operation.Action, remote.State) {
			return executor.succeed(ctx, operation, remote)
		}
		if time.Now().After(deadline) {
			timedOut, transitionErr := executor.operations.Transition(ctx, operation.ID, StatusTimedOut, Transition{
				At: executor.options.Now(), ErrorCode: "operation_timeout", ErrorMessage: "Provider state verification timed out",
			})
			if transitionErr != nil {
				return transitionErr
			}
			executor.publishOperation(timedOut)
			return executor.appendAudit(ctx, operation, "power_operation_timed_out", StatusTimedOut)
		}
		timer := time.NewTimer(executor.options.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (executor *Executor) provider(ctx context.Context, connectionID string) (providers.Provider, error) {
	connection, stored, err := executor.connections.FindByID(ctx, connectionID)
	if err != nil {
		return nil, err
	}
	plaintext, err := executor.cipher.Decrypt(connection.ID, connection.ProviderType, secrets.Envelope{
		Ciphertext: stored.Ciphertext, Nonce: stored.Nonce, KeyVersion: stored.KeyVersion,
	})
	if err != nil {
		return nil, fmt.Errorf("decrypt provider credentials: %w", err)
	}
	defer clear(plaintext)
	return executor.registry.Create(providers.ConnectionConfig{
		ID: connection.ID, Type: connection.ProviderType, Endpoint: connection.Endpoint,
		Settings: connection.Settings, Credentials: plaintext,
	})
}

func executeProviderAction(ctx context.Context, provider providers.Provider, ref providers.ServerRef, action Action) (providers.ActionReceipt, error) {
	switch action {
	case ActionStart:
		return provider.StartServer(ctx, ref)
	case ActionStop:
		return provider.StopServer(ctx, ref)
	case ActionReboot:
		return provider.RebootServer(ctx, ref)
	default:
		return providers.ActionReceipt{}, ErrInvalidAction
	}
}

func actionAlreadySatisfied(action Action, state providers.ServerState) bool {
	switch action {
	case ActionStart:
		return state == providers.StateRunning
	case ActionStop:
		return state == providers.StateStopped
	default:
		return false
	}
}

func verificationTargetReached(action Action, state providers.ServerState) bool {
	if action == ActionReboot {
		return state == providers.StateRunning
	}
	return actionAlreadySatisfied(action, state)
}

func (executor *Executor) succeed(ctx context.Context, operation Operation, remote providers.RemoteServer) error {
	if err := executor.inventory.UpdateRemote(ctx, operation.ServerID, remote, executor.options.Now().UTC()); err != nil {
		return err
	}
	executor.publishServer(operation.ServerID)
	succeeded, err := executor.operations.Transition(ctx, operation.ID, StatusSucceeded, Transition{At: executor.options.Now()})
	if err != nil {
		return err
	}
	executor.publishOperation(succeeded)
	return executor.appendAudit(ctx, operation, "power_operation_succeeded", StatusSucceeded)
}

func (executor *Executor) finishError(ctx context.Context, operation Operation, job jobs.Job, executionErr error) error {
	code, message, retryable := operationError(executionErr)
	if retryable && job.Attempts < job.MaxAttempts {
		return executionErr
	}
	current, findErr := executor.operations.FindByID(ctx, operation.ID)
	if findErr != nil {
		return errors.Join(executionErr, findErr)
	}
	if !current.Status.terminal() {
		failed, err := executor.operations.Transition(ctx, operation.ID, StatusFailed, Transition{
			At: executor.options.Now(), ErrorCode: code, ErrorMessage: message,
		})
		if err != nil {
			return errors.Join(executionErr, err)
		}
		executor.publishOperation(failed)
		if err := executor.appendAudit(ctx, operation, "power_operation_failed", StatusFailed); err != nil {
			return errors.Join(executionErr, err)
		}
	}
	return executionErr
}

func (executor *Executor) publishOperation(operation Operation) {
	if executor.options.Publisher == nil {
		return
	}
	data, _ := json.Marshal(map[string]string{
		"operation_id": operation.ID, "server_id": operation.ServerID, "status": string(operation.Status),
	})
	executor.options.Publisher.Publish(events.Event{Type: "operation.updated", Data: data})
}

func (executor *Executor) publishServer(serverID string) {
	if executor.options.Publisher == nil {
		return
	}
	data, _ := json.Marshal(map[string]string{"server_id": serverID})
	executor.options.Publisher.Publish(events.Event{Type: "server.updated", Data: data})
}

func operationError(err error) (string, string, bool) {
	var providerError *providers.Error
	if errors.As(err, &providerError) {
		return string(providerError.Code), providerError.Message, providerError.Retryable
	}
	return string(providers.ErrorProvider), "Power operation failed", false
}

func (executor *Executor) appendAudit(ctx context.Context, operation Operation, eventType string, status Status) error {
	metadata, _ := json.Marshal(map[string]string{"action": string(operation.Action), "status": string(status)})
	return executor.audit.Append(ctx, audit.Entry{
		ID: executor.options.NewAuditID(), EventType: eventType, TargetType: "operation", TargetID: operation.ID,
		Metadata: metadata, CreatedAt: executor.options.Now().UTC(),
	})
}
