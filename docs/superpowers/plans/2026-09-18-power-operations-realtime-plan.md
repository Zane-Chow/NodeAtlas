# Power Operations and Realtime State Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the administrator safely start, stop, and reboot synchronized servers through persistent, idempotent operations, with audit history and realtime SSE updates in the existing interface.

**Architecture:** A new `operations` domain owns the operation state machine, portable active-operation uniqueness, validation, provider execution, and REST API. Power work is enqueued in the existing database-backed job system and executed by the existing worker; an in-memory event broker emits non-sensitive operation and inventory invalidation events through an authenticated SSE endpoint. The React client confirms mutations, renders operation history, and refetches local indexed data after events.

**Tech Stack:** Go 1.27, Chi v5, `database/sql`, SQLite/MySQL, React 19, TypeScript 6, Vite, Vitest, Testing Library, Server-Sent Events.

**Spec:** `docs/superpowers/specs/2026-09-17-server-control-panel-design.md` sections 8.5, 8.8, 13, 16.3, 17, 18, 19, and stage-one milestone 3 in `docs/superpowers/plans/2026-09-17-stage-one-roadmap.md`.

## Global Constraints

- Only `start`, `stop`, and `reboot` are supported; there are no bulk operations.
- Every mutating endpoint uses the existing session, origin, and CSRF protections.
- A server can have at most one non-terminal operation, enforced in both SQLite and MySQL.
- An idempotency key returns the previously created operation and never queues a duplicate provider call.
- The worker re-reads remote state before a write and does not repeat a write when the target state is already satisfied.
- Provider authentication and permission errors are terminal; rate limits, network errors, and provider 5xx-style errors receive bounded job retries.
- Operation and audit responses never expose encrypted credentials, provider tokens, cookies, or raw provider response bodies.
- SSE carries invalidation/status events only; pages continue to read authoritative state from local REST APIs.
- All identifiers are UUIDs and all persisted timestamps are UTC/RFC 3339.
- Repository contracts run against SQLite and, when `TEST_MYSQL_URL` is present, MySQL.

---

### Task 1: Operation and Audit Persistence

**Files:**
- Modify: `internal/database/migrations.go`
- Modify: `internal/database/database_test.go`
- Create: `internal/operations/model.go`
- Create: `internal/operations/repository.go`
- Create: `internal/operations/sql_repository.go`
- Create: `internal/operations/repository_contract_test.go`
- Create: `internal/audit/model.go`
- Create: `internal/audit/repository.go`
- Create: `internal/audit/sql_repository.go`
- Create: `internal/audit/repository_contract_test.go`

**Interfaces:**
- Produces: `operations.Repository` with atomic create/idempotency, state transitions, lookup, and list.
- Produces: `audit.Repository.Append(context.Context, audit.Entry) error` and `List(context.Context, audit.Filter) ([]audit.Entry, error)`.
- Produces: migration version 3 containing `operations` and append-only `audit_logs`.

- [x] **Step 1: Write failing migration and repository contract tests**

```go
func TestCreateQueuedIsIdempotentAndRejectsSecondActiveOperation(t *testing.T) {
    repository := openOperationRepository(t)
    first, created, err := repository.CreateQueued(context.Background(), queuedOperation("server-a", "idem-a"))
    require.NoError(t, err)
    require.True(t, created)
    same, created, err := repository.CreateQueued(context.Background(), queuedOperation("server-a", "idem-a"))
    require.NoError(t, err)
    require.False(t, created)
    require.Equal(t, first.ID, same.ID)
    _, _, err = repository.CreateQueued(context.Background(), queuedOperation("server-a", "idem-b"))
    require.ErrorIs(t, err, ErrActiveOperation)
}
```

Also prove valid transition ordering, terminal operations releasing the active marker, newest-first listing, operation lookup, and immutable audit append/list behavior.

- [x] **Step 2: Run tests and verify red**

Run: `go test ./internal/database ./internal/operations ./internal/audit -v`

Expected: FAIL because migration version 3 and both packages do not exist.

- [x] **Step 3: Add portable migration version 3**

Create `operations` with the approved public fields plus a nullable `active_marker`. Set `active_marker = 'active'` for `queued`, `running`, and `verifying`; set it to `NULL` for terminal states. Add `UNIQUE(server_id, active_marker)` so both databases enforce one active operation while allowing multiple terminal rows. Make `idempotency_key` non-empty and unique. Add indexes for newest-first history and connection status queries.

Create append-only `audit_logs` with JSON text metadata, request/source fields, and indexes on creation time and target. Do not expose update/delete repository methods.

- [x] **Step 4: Implement models and repositories**

```go
type Action string
const (ActionStart Action = "start"; ActionStop Action = "stop"; ActionReboot Action = "reboot")

type Status string
const (
    StatusQueued Status = "queued"
    StatusRunning Status = "running"
    StatusVerifying Status = "verifying"
    StatusSucceeded Status = "succeeded"
    StatusFailed Status = "failed"
    StatusTimedOut Status = "timed_out"
    StatusCancelled Status = "cancelled"
)

type Repository interface {
    CreateQueued(context.Context, Operation) (Operation, bool, error)
    FindByID(context.Context, string) (Operation, error)
    List(context.Context, Filter) ([]Operation, error)
    Transition(context.Context, string, Status, Transition) (Operation, error)
}
```

`CreateQueued` performs the idempotency lookup and insert in one transaction. Translate the `(server_id, active_marker)` unique violation to `ErrActiveOperation` and the idempotency unique violation to a lookup of the existing row. `Transition` validates the state graph before updating and clears `active_marker` for terminal states.

- [x] **Step 5: Verify and commit**

Run: `go test ./internal/database ./internal/operations ./internal/audit -v`

Expected: PASS on SQLite; MySQL contracts run when configured.

```bash
git add internal/database internal/operations internal/audit
git commit -m "feat: persist idempotent power operations and audit events"
```

---

### Task 2: Idempotent Operation Queueing and Validation Service

**Files:**
- Modify: `internal/jobs/model.go`
- Modify: `internal/jobs/queue.go`
- Create: `internal/jobs/queue_test.go`
- Create: `internal/operations/service.go`
- Create: `internal/operations/service_test.go`

**Interfaces:**
- Consumes: inventory lookup, operation repository, audit append, and persistent jobs.
- Produces: `operations.Service.Request(context.Context, Request) (Operation, bool, error)`.
- Produces: `jobs.KindPowerOperation` and `Queue.EnqueuePowerOperation(context.Context, string) error`.

- [x] **Step 1: Write failing service tests**

```go
func TestRequestStartQueuesOneIdempotentOperation(t *testing.T) {
    fixture := newServiceFixture(t, stoppedServerWithStartCapability())
    first, created, err := fixture.service.Request(context.Background(), Request{
        ServerID: "server-a", Action: ActionStart, IdempotencyKey: "idem-a", RequestID: "request-a",
    })
    require.NoError(t, err)
    require.True(t, created)
    second, created, err := fixture.service.Request(context.Background(), Request{
        ServerID: "server-a", Action: ActionStart, IdempotencyKey: "idem-a", RequestID: "request-b",
    })
    require.NoError(t, err)
    require.False(t, created)
    require.Equal(t, first.ID, second.ID)
    require.Equal(t, []string{first.ID}, fixture.queue.operationIDs)
}
```

Also cover invalid actions, missing server, disabled capability, current-state conflict, active-operation conflict, generated idempotency keys, and a sanitized `power_operation_queued` audit entry.

- [x] **Step 2: Run tests and verify red**

Run: `go test ./internal/operations ./internal/jobs -run 'Service|Queue' -v`

Expected: FAIL because the service and power job kind do not exist.

- [x] **Step 3: Add the power job kind and service**

```go
const KindPowerOperation Kind = "power_operation"

type Request struct {
    ServerID string
    Action Action
    IdempotencyKey string
    RequestID string
    SourceIP string
}
```

Parse `inventory.Server.Capabilities` into `providers.Capabilities`, validate the action against capability and state, create the queued operation, enqueue only when `created == true`, and append audit metadata containing only action and operation status. If enqueue fails, transition the newly created operation to `failed` so it does not hold the active marker.

- [x] **Step 4: Verify and commit**

Run: `go test ./internal/operations ./internal/jobs ./internal/inventory -v`

Expected: PASS.

```bash
git add internal/jobs internal/inventory internal/operations
git commit -m "feat: validate and queue server power operations"
```

---

### Task 3: Provider Execution and Final-State Verification

**Files:**
- Modify: `internal/inventory/repository.go`
- Modify: `internal/inventory/sql_repository.go`
- Modify: `internal/inventory/repository_contract_test.go`
- Modify: `internal/providers/mock/provider.go`
- Modify: `internal/providers/mock/provider_test.go`
- Create: `internal/operations/executor.go`
- Create: `internal/operations/executor_test.go`

**Interfaces:**
- Consumes: operation, inventory, connection, credential cipher, and provider registry.
- Produces: `(*Executor).Execute(context.Context, jobs.Job) error` for `jobs.KindPowerOperation`.
- Produces: `inventory.Repository.UpdateRemote(context.Context, string, providers.RemoteServer, time.Time) error`.

- [x] **Step 1: Write failing executor tests**

```go
func TestExecutorSkipsWriteWhenRemoteAlreadyMatchesTarget(t *testing.T) {
    fixture := newExecutorFixture(t, ActionStart, providers.StateRunning)
    err := fixture.executor.Execute(context.Background(), fixture.job())
    require.NoError(t, err)
    require.Zero(t, fixture.provider.startCalls)
    require.Equal(t, StatusSucceeded, fixture.operations.current.Status)
}
```

Also cover provider call then verification success, reboot requiring a running final state, authentication failure, retryable network failure, bounded verification timeout, malformed payload, credentials decryption, remote inventory refresh, sanitized completion/failure audit events, and Mock state remaining changed when the registry creates another provider instance for the same connection.

- [x] **Step 2: Run tests and verify red**

Run: `go test ./internal/operations ./internal/inventory -run 'Executor|UpdateRemote' -v`

Expected: FAIL because the executor and single-server update are absent.

- [x] **Step 3: Implement execution state machine**

On each attempt, load the operation and skip terminal rows. Transition to `running`, construct the provider from decrypted connection credentials, and call `GetServer`. For start/stop, finish immediately if the remote state already matches. Otherwise call the correct provider action once, store the non-sensitive request ID, transition to `verifying`, and poll `GetServer` with configurable intervals until the expected state, terminal provider error, or deadline.

On success, update the local server state/capabilities and transition to `succeeded`. On retryable errors before the job's final attempt, leave the operation non-terminal and return the provider error to the worker. On the final attempt mark `failed`; verification deadline marks `timed_out`. Authentication, permission, unsupported, not-found, and invalid-configuration errors immediately mark `failed`.

Change the Mock factory to own a mutex-protected map of per-connection server state. Recreating a Mock provider for the same connection reuses that state, while different connection IDs remain isolated. Configuration changes regenerate only that connection's fixture when its deterministic settings fingerprint changes. This makes Mock power behavior equivalent to a persistent remote provider across sync and operation jobs.

- [x] **Step 4: Verify and commit**

Run: `go test ./internal/operations ./internal/inventory ./internal/providers/... -v`

Expected: PASS.

```bash
git add internal/operations internal/inventory internal/providers/mock
git commit -m "feat: execute and verify provider power operations"
```

---

### Task 4: Operation REST API and Realtime Event Broker

**Files:**
- Create: `internal/events/broker.go`
- Create: `internal/events/broker_test.go`
- Create: `internal/events/http.go`
- Create: `internal/events/http_test.go`
- Create: `internal/operations/http.go`
- Create: `internal/operations/http_test.go`

**Interfaces:**
- Produces: authenticated `POST /servers/{id}/actions/{action}`, `GET /operations`, and `GET /operations/{id}`.
- Produces: authenticated `GET /events` using `text/event-stream`.
- Produces: `events.Publisher.Publish(Event)` and bounded per-client subscriptions.

- [x] **Step 1: Write failing HTTP and broker tests**

```go
func TestActionEndpointUsesIdempotencyKey(t *testing.T) {
    request := jsonRequest(http.MethodPost, "/servers/server-a/actions/start", `{}`)
    request.Header.Set("Idempotency-Key", "idem-a")
    response := httptest.NewRecorder()
    handler.ServeHTTP(response, request)
    require.Equal(t, http.StatusAccepted, response.Code)
    require.Contains(t, response.Body.String(), `"status":"queued"`)
}
```

Cover invalid action, capability/state conflict (`409`), active operation (`409`), idempotent replay (`200`), not found (`404`), list/detail, and stable error codes. Broker tests prove ordered event delivery, slow subscribers being disconnected rather than blocking publishers, cancellation cleanup, SSE headers, event IDs, heartbeat comments, and JSON payloads without credentials.

- [x] **Step 2: Run tests and verify red**

Run: `go test ./internal/operations ./internal/events -run 'HTTP|Broker' -v`

Expected: FAIL because HTTP handlers and events package do not exist.

- [x] **Step 3: Implement API and SSE**

Use `Idempotency-Key` when present; otherwise generate a UUID in the service. Return `{ "operation": ... }` with `202` for new operations and `200` for replay. Expose newest-first operation history with optional `server_id`, `connection_id`, and `status` filters.

The broker assigns monotonic in-process IDs and uses a fixed-size channel per subscriber. `GET /events` sends `retry: 3000`, typed `operation.updated`/`server.updated` events, and 15-second heartbeat comments. Never include provider payloads or secrets. Disconnect a subscriber whose channel is full.

- [x] **Step 4: Verify and commit**

Run: `go test ./internal/operations ./internal/events -v`

Expected: PASS.

```bash
git add internal/operations internal/events
git commit -m "feat: expose power operations and realtime events"
```

---

### Task 5: Application Composition and End-to-End Backend Flow

**Files:**
- Modify: `internal/app/app.go`
- Modify: `internal/app/app_test.go`
- Modify: `internal/auth/http_test.go`

**Interfaces:**
- Consumes: operation service/executor/HTTP, event broker/HTTP, audit repository, and both job kinds.
- Produces: a single composed application in which authenticated action requests are executed by the worker and visible through operations/inventory APIs and SSE.

- [x] **Step 1: Write a failing composed-flow test**

```go
func TestPowerOperationFlowsThroughAuthenticatedApplication(t *testing.T) {
    fixture := newAuthenticatedAppFixture(t)
    serverID := fixture.syncMockServer(t, providers.StateStopped)
    operation := fixture.postAction(t, serverID, "start", "idem-app")
    fixture.runWorkerUntilIdle(t)
    require.Equal(t, "succeeded", fixture.getOperation(t, operation.ID).Status)
    require.Equal(t, "running", fixture.getServer(t, serverID).State)
}
```

Also assert unauthenticated access is rejected, mutation security is enforced, replay queues no second job, and `/events` is behind authentication.

- [x] **Step 2: Run test and verify red**

Run: `go test ./internal/app -run PowerOperation -v`

Expected: FAIL because the handlers and worker dispatch are not composed.

- [x] **Step 3: Wire the milestone into `compose`**

Register repositories and services once, mount operation/event routes in the protected feature dispatcher, and dispatch `sync_connection` to the syncer and `power_operation` to the executor. Publish operation/server invalidations after queueing and state transitions through injected publisher hooks.

- [x] **Step 4: Verify and commit**

Run: `go test ./internal/app ./internal/auth ./internal/... -v`

Expected: PASS.

```bash
git add internal/app internal/auth
git commit -m "feat: compose authenticated power operation workflow"
```

---

### Task 6: Power Controls, Operation History, and SSE React Client

**Files:**
- Create: `web/src/operations/types.ts`
- Create: `web/src/operations/api.ts`
- Create: `web/src/operations/OperationsPage.tsx`
- Create: `web/src/operations/OperationsPage.test.tsx`
- Create: `web/src/events/useServerEvents.ts`
- Create: `web/src/events/useServerEvents.test.tsx`
- Modify: `web/src/servers/ServersPage.tsx`
- Modify: `web/src/servers/ServersPage.test.tsx`
- Modify: `web/src/app/AppShell.tsx`
- Modify: `web/src/app/App.test.tsx`
- Modify: `web/src/features.css`

**Interfaces:**
- Consumes: power action, operation history, inventory, and SSE endpoints.
- Produces: confirmed capability-aware power actions, operation history navigation, and realtime local-data refresh.

- [ ] **Step 1: Write failing server action tests**

```tsx
it('confirms and queues a supported start action', async () => {
  render(<ServersPage />)
  await user.click(await screen.findByRole('button', { name: '查看 web-01' }))
  await user.click(screen.getByRole('button', { name: '开机' }))
  expect(await screen.findByRole('dialog', { name: '确认开机' })).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: '确认开机' }))
  await waitFor(() => expect(fetch).toHaveBeenCalledWith(
    '/api/v1/servers/server-a/actions/start', expect.objectContaining({ method: 'POST' }),
  ))
})
```

Cover unsupported/invalid-state disabled buttons with capability reasons, cancel, API conflict/error, optimistic busy state without optimistic final state, and successful refetch after an event.

- [ ] **Step 2: Write failing history and SSE tests**

Test operation status/action labels, newest-first rows, server filters, empty/error/loading states, navigation to `/operations`, EventSource lifecycle, reconnect behavior delegated to the browser, and refetch callbacks for `operation.updated` and `server.updated` only.

- [ ] **Step 3: Run tests and verify red**

Run: `npm --prefix web test -- --run src/servers/ServersPage.test.tsx src/operations src/events src/app/App.test.tsx`

Expected: FAIL because action/history/event components do not exist.

- [ ] **Step 4: Implement typed client and UI**

Add `requestPowerAction(serverID, action, idempotencyKey)`, `listOperations(filters)`, and `getOperation(id)`. Generate one idempotency key per confirmation submission with `crypto.randomUUID()`. Buttons derive state and explanations exclusively from returned capabilities. Keep final server state authoritative by refetching after the mutation and SSE invalidation.

Enable the existing “操作记录” navigation item and route. Operation history shows action, target server, status, queue/start/finish times, and sanitized error text. `useServerEvents` creates one authenticated same-origin `EventSource('/api/v1/events')`, closes it on unmount, and exposes invalidation callbacks.

- [ ] **Step 5: Verify production frontend and commit**

Run:

```bash
npm --prefix web test -- --run
npm --prefix web run lint
npm --prefix web run build
rsync -a --delete web/dist/ internal/webassets/dist/
```

Expected: all tests and lint pass; embedded assets match the build.

```bash
git add web internal/webassets/dist
git commit -m "feat: add realtime power controls and operation history"
```

---

### Task 7: Milestone Verification and Documentation

**Files:**
- Modify: `README.md`
- Create: `docs/operations.md`

**Interfaces:**
- Produces: operator documentation for action validation, idempotency, retries, audit history, and SSE.

- [ ] **Step 1: Document behavior and failure semantics**

Document confirmation requirements, supported state transitions, disabled reasons, idempotency keys, worker retry limits, verification timeouts, operation statuses, event-stream proxy requirements, and the fact that console controls remain in the next milestone.

- [ ] **Step 2: Run complete verification**

Run:

```bash
go test ./...
npm --prefix web test -- --run
npm --prefix web run lint
npm --prefix web run build
docker build --progress=plain -t controlpanel-power-operations .
```

Start an isolated SQLite container, initialize the administrator, create a Mock connection containing a stopped server, synchronize it, submit `start`, replay the same idempotency key, and assert one operation reaches `succeeded` and the server becomes `running`. Repeat repository/migration verification with MySQL or `TEST_MYSQL_URL`.

- [ ] **Step 3: Verify non-disclosure and cleanup**

Search responses, logs, audit metadata, SSE payloads, and rendered assets for the submitted Mock token. Confirm no temporary containers or listeners remain and `git diff --check` is clean.

- [ ] **Step 4: Commit**

```bash
git add README.md docs/operations.md
git commit -m "docs: document power operations and realtime updates"
```
