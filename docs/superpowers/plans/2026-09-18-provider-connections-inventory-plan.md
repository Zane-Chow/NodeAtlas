# Provider Connections, Mock Provider, and Unified Inventory Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the administrator create multiple encrypted Mock provider connections, test and synchronize them through a persistent job queue, and browse the resulting normalized server inventory from the authenticated interface.

**Architecture:** New `secrets`, `providers`, `connections`, `jobs`, and `inventory` packages extend the modular Go application. Provider credentials are encrypted before persistence; a registry constructs providers without leaking provider-specific types into services. Synchronization runs through database-backed jobs and writes a local server index consumed by REST APIs and React pages.

**Tech Stack:** Go 1.27, Chi v5, `database/sql`, AES-256-GCM, SQLite/MySQL, React 19, TypeScript 6, Vite, Vitest, Testing Library.

**Spec:** `docs/superpowers/specs/2026-09-17-server-control-panel-design.md`

## Global Constraints

- A provider type is not unique; the administrator may create any number of independent connections of the same type.
- Credentials are write-only and encrypted with AES-256-GCM using a 32-byte application master key that is never stored in the database.
- Connection ID, provider type, and key version are authenticated as encryption additional data.
- Mock is a real Provider implementation and uses the same registry, connection service, synchronization path, and inventory model as future adapters.
- List pages read the local database index and never call providers directly.
- An unsuccessful synchronization never hides servers from the last complete synchronization.
- All IDs are UUIDs, persisted timestamps are UTC, and API timestamps are RFC 3339.
- SQLite and MySQL receive equivalent migrations and repository contracts.
- Mutating HTTP endpoints require the existing session, origin, and CSRF protections.
- API responses never include credential plaintext, ciphertext, nonce, key material, or provider console secrets.

---

### Task 1: Master Key Configuration and Credential Encryption

**Files:**
- Create: `internal/secrets/credentials.go`
- Create: `internal/secrets/credentials_test.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `.env.example`

**Interfaces:**
- Produces: `secrets.NewCredentialCipher(map[int][]byte, int) (*CredentialCipher, error)`.
- Produces: `(*CredentialCipher).Encrypt(connectionID, providerType string, plaintext []byte) (Envelope, error)`.
- Produces: `(*CredentialCipher).Decrypt(connectionID, providerType string, envelope Envelope) ([]byte, error)`.
- Produces: `config.Config.Secrets` with an active key version and decoded 32-byte keys.

- [ ] **Step 1: Write failing cipher and configuration tests**

```go
func TestCredentialCipherRoundTripAndAADBinding(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	cipher, err := NewCredentialCipher(map[int][]byte{1: key}, 1)
	require.NoError(t, err)
	envelope, err := cipher.Encrypt("connection-a", "mock", []byte(`{"token":"secret"}`))
	require.NoError(t, err)
	plaintext, err := cipher.Decrypt("connection-a", "mock", envelope)
	require.NoError(t, err)
	require.JSONEq(t, `{"token":"secret"}`, string(plaintext))
	_, err = cipher.Decrypt("connection-b", "mock", envelope)
	require.Error(t, err)
}

func TestLoadRequiresCredentialKey(t *testing.T) {
	t.Setenv("CREDENTIAL_KEYS", "")
	_, err := Load()
	require.ErrorContains(t, err, "CREDENTIAL_KEYS")
}
```

- [ ] **Step 2: Run tests to verify red**

Run: `go test ./internal/secrets ./internal/config -v`
Expected: FAIL because the secrets package and configuration fields do not exist.

- [ ] **Step 3: Implement the key ring and AES-256-GCM envelope**

```go
type Envelope struct {
	Ciphertext []byte
	Nonce      []byte
	KeyVersion int
}

type CredentialCipher struct {
	keys          map[int][]byte
	activeVersion int
}

func additionalData(connectionID, providerType string, version int) []byte {
	return []byte(fmt.Sprintf("controlpanel:credentials:v%d:%s:%s", version, providerType, connectionID))
}
```

Parse `CREDENTIAL_KEYS` as comma-separated `version:base64` entries and `CREDENTIAL_ACTIVE_KEY_VERSION` as an integer. Require exactly 32 decoded bytes per key and require the active version to exist. Development may use `CREDENTIAL_KEYS=1:<base64>`; there is no built-in key.

- [ ] **Step 4: Run tests to verify green**

Run: `go test ./internal/secrets ./internal/config -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/secrets internal/config .env.example
git commit -m "feat: encrypt provider credentials with versioned keys"
```

---

### Task 2: Connection, Server, and Job Persistence

**Files:**
- Modify: `internal/database/migrations.go`
- Modify: `internal/database/database_test.go`
- Create: `internal/connections/model.go`
- Create: `internal/connections/repository.go`
- Create: `internal/connections/sql_repository.go`
- Create: `internal/connections/repository_contract_test.go`
- Create: `internal/inventory/model.go`
- Create: `internal/inventory/repository.go`
- Create: `internal/inventory/sql_repository.go`
- Create: `internal/inventory/repository_contract_test.go`
- Create: `internal/jobs/model.go`
- Create: `internal/jobs/repository.go`
- Create: `internal/jobs/sql_repository.go`
- Create: `internal/jobs/repository_contract_test.go`

**Interfaces:**
- Produces: connection create, update, soft-delete, health, and sync timestamp repository methods.
- Produces: inventory transactional `ApplyCompleteSync` and list/detail methods.
- Produces: persistent job enqueue, lease, complete, retry, and fail methods.

- [ ] **Step 1: Write failing migration and repository contract tests**

```go
func TestMilestoneTwoTablesExist(t *testing.T) {
	db, dialect := openSQLiteTestDB(t)
	require.NoError(t, Migrate(context.Background(), db, dialect))
	for _, table := range []string{"provider_connections", "servers", "jobs"} {
		requireTableExists(t, db, table)
	}
}
```

Repository contracts must prove that two `mock` connections coexist, credentials never appear on returned public models, `(connection_id, scope, external_id)` is unique, a complete sync hides missing servers, a failed transaction preserves the prior visible set, and an expired job lease can be reclaimed.

- [ ] **Step 2: Run tests to verify red**

Run: `go test ./internal/database ./internal/connections ./internal/inventory ./internal/jobs -v`
Expected: FAIL because migration version 2 and repositories do not exist.

- [ ] **Step 3: Add equivalent SQLite and MySQL migration version 2**

Create `provider_connections`, `servers`, and `jobs` exactly as specified by sections 8.3, 8.4, and 8.6 of the design. Store JSON as text for dialect parity; add indexes for connection type, visible servers, job availability, and lease expiry. Enforce the inventory unique key and foreign keys to connections.

- [ ] **Step 4: Implement focused SQL repositories**

```go
type CredentialRecord struct {
	Ciphertext []byte
	Nonce []byte
	KeyVersion int
}

type SyncSnapshot struct {
	ConnectionID string
	CompletedAt time.Time
	Servers []Server
}

type Lease struct {
	Job Job
	Owner string
	ExpiresAt time.Time
}
```

Use transactions for connection deletion, complete inventory application, and job leasing. Translate duplicate keys to package errors and use dialect-neutral `?` placeholders supported by both configured drivers.

- [ ] **Step 5: Run repository contracts against SQLite and opt-in MySQL**

Run: `go test ./internal/database ./internal/connections ./internal/inventory ./internal/jobs -v`
Expected: PASS; MySQL contracts skip unless `TEST_MYSQL_URL` is set.

- [ ] **Step 6: Commit**

```bash
git add internal/database internal/connections internal/inventory internal/jobs
git commit -m "feat: persist provider connections inventory and jobs"
```

---

### Task 3: Provider Contract, Registry, and Mock Adapter

**Files:**
- Create: `internal/providers/provider.go`
- Create: `internal/providers/registry.go`
- Create: `internal/providers/registry_test.go`
- Create: `internal/providers/contract_test.go`
- Create: `internal/providers/mock/provider.go`
- Create: `internal/providers/mock/provider_test.go`

**Interfaces:**
- Produces: the unified `providers.Provider` contract from design section 5.
- Produces: `providers.Registry.Register` and `providers.Registry.Create`.
- Produces: deterministic Mock instances configured from connection settings and encrypted credentials.

- [ ] **Step 1: Write failing registry and Provider contract tests**

```go
func TestRegistryCreatesIndependentMockConnections(t *testing.T) {
	registry := NewRegistry()
	require.NoError(t, registry.Register("mock", mock.NewFactory()))
	first, err := registry.Create(ConnectionConfig{ID: "one", Type: "mock", Settings: json.RawMessage(`{"server_count":2,"seed":1}`)})
	require.NoError(t, err)
	second, err := registry.Create(ConnectionConfig{ID: "two", Type: "mock", Settings: json.RawMessage(`{"server_count":3,"seed":2}`)})
	require.NoError(t, err)
	firstPage, _ := first.ListServers(context.Background(), nil)
	secondPage, _ := second.ListServers(context.Background(), nil)
	require.Len(t, firstPage.Servers, 2)
	require.Len(t, secondPage.Servers, 3)
}
```

Run the same reusable contract suite for each registered adapter: validate, paginate, get, stable IDs, normalized states, capability consistency, and portal URL safety.

- [ ] **Step 2: Run tests to verify red**

Run: `go test ./internal/providers/... -v`
Expected: FAIL because the Provider types and Mock factory do not exist.

- [ ] **Step 3: Implement the unified contract and registry**

```go
type Provider interface {
	ValidateConnection(context.Context) (ConnectionInfo, error)
	ListServers(context.Context, *Cursor) (ServerPage, error)
	GetServer(context.Context, ServerRef) (RemoteServer, error)
	StartServer(context.Context, ServerRef) (ActionReceipt, error)
	StopServer(context.Context, ServerRef) (ActionReceipt, error)
	RebootServer(context.Context, ServerRef) (ActionReceipt, error)
	OpenConsole(context.Context, ServerRef, ConsoleMode) (ConsoleTarget, error)
	ProviderPortalURL(context.Context, ServerRef) (*url.URL, error)
}

type Factory interface {
	Create(ConnectionConfig) (Provider, error)
}
```

Registry registration rejects empty and duplicate names. Creation rejects unknown types with a stable `ErrUnknownProvider`.

- [ ] **Step 4: Implement configurable Mock behavior**

Mock settings include `server_count`, `seed`, `operation_delay_ms`, `failure_rate`, `health_mode`, and `console_profile`. Generate stable external IDs and addresses from connection ID plus seed. Implement healthy, authentication failure, network failure, and rate-limited validation modes. Console profiles are `embedded`, `window`, `portal`, and `none`.

- [ ] **Step 5: Verify and commit**

Run: `go test ./internal/providers/... -v`
Expected: PASS.

```bash
git add internal/providers
git commit -m "feat: add provider registry and contract-compliant mock"
```

---

### Task 4: Connection Service and Authenticated REST API

**Files:**
- Create: `internal/connections/service.go`
- Create: `internal/connections/service_test.go`
- Create: `internal/connections/http.go`
- Create: `internal/connections/http_test.go`
- Modify: `internal/auth/http.go`
- Modify: `internal/httpapi/router.go`
- Modify: `internal/app/app.go`

**Interfaces:**
- Consumes: credential cipher, connection repository, Provider registry, and job repository.
- Produces: authenticated `/api/v1/provider-types` and `/api/v1/connections` endpoints.
- Produces: reusable auth middleware shared by feature routers.

- [ ] **Step 1: Write failing service tests**

```go
func TestCreateEncryptsCredentialsAndQueuesInitialSync(t *testing.T) {
	service := newServiceFixture(t)
	created, err := service.Create(context.Background(), CreateInput{
		Name: "Lab A", ProviderType: "mock",
		Settings: json.RawMessage(`{"server_count":4,"seed":9}`),
		Credentials: json.RawMessage(`{"token":"write-only"}`),
	})
	require.NoError(t, err)
	require.Equal(t, "mock", created.ProviderType)
	require.NotContains(t, string(service.connectionStore.rawCredentials), "write-only")
	require.Equal(t, "sync_connection", service.jobStore.last.Kind)
}
```

Also cover duplicate display names being allowed, unsupported providers, invalid Mock settings, test failures mapped to stable health codes, credentials omitted on update retaining the old envelope, and disabled connections not queuing sync.

- [ ] **Step 2: Run service tests to verify red**

Run: `go test ./internal/connections -run Service -v`
Expected: FAIL because the service does not exist.

- [ ] **Step 3: Implement connection orchestration**

```go
type CreateInput struct {
	Name string
	ProviderType string
	Endpoint string
	Settings json.RawMessage
	Credentials json.RawMessage
	Enabled bool
}

func (s *Service) Create(context.Context, CreateInput) (Connection, error)
func (s *Service) Update(context.Context, string, UpdateInput) (Connection, error)
func (s *Service) Test(context.Context, string) (TestResult, error)
func (s *Service) RequestSync(context.Context, string) (jobs.Job, error)
```

Create the UUID before encryption so it participates in AAD. Provider testing decrypts only in memory and clears plaintext byte slices after factory creation.

- [ ] **Step 4: Write failing HTTP tests**

Test `GET /provider-types`, `GET/POST /connections`, `GET/PUT/DELETE /connections/{id}`, `POST /connections/{id}/test`, and `POST /connections/{id}/sync`. Prove unauthenticated requests return 401, mutations reject missing CSRF, errors use the existing envelope, and serialized connections contain no credential fields.

- [ ] **Step 5: Implement and mount the authenticated API**

Expose the existing session/origin/CSRF middleware as an authenticated subrouter builder. Use 64 KiB strict JSON decoding, return `202 Accepted` for queued sync, and return `204 No Content` for deletion.

- [ ] **Step 6: Verify and commit**

Run: `go test ./internal/connections ./internal/auth ./internal/httpapi -v`
Expected: PASS.

```bash
git add internal/connections internal/auth internal/httpapi internal/app
git commit -m "feat: expose encrypted provider connection management"
```

---

### Task 5: Persistent Worker and Inventory Synchronization

**Files:**
- Create: `internal/jobs/worker.go`
- Create: `internal/jobs/worker_test.go`
- Create: `internal/inventory/sync.go`
- Create: `internal/inventory/sync_test.go`
- Modify: `internal/app/app.go`

**Interfaces:**
- Consumes: leased `sync_connection` jobs, decrypted provider connections, Provider registry, and inventory repository.
- Produces: bounded-retry workers and complete synchronization semantics.

- [ ] **Step 1: Write failing synchronization tests**

```go
func TestSyncAppliesAllPagesOnlyAfterCompleteSuccess(t *testing.T) {
	provider := &pagedProvider{pages: []providers.ServerPage{
		{Servers: []providers.RemoteServer{{ExternalID: "one"}}, Next: &providers.Cursor{Value: "next"}},
		{Servers: []providers.RemoteServer{{ExternalID: "two"}}},
	}}
	syncer := newSyncFixture(provider)
	require.NoError(t, syncer.SyncConnection(context.Background(), "connection-id"))
	require.Equal(t, []string{"one", "two"}, syncer.inventory.visibleExternalIDs())
}
```

Add tests for a second-page failure preserving prior inventory, disabled/deleted connections cancelling work, normalized error codes updating connection health, exponential retry scheduling, and worker shutdown returning leases safely.

- [ ] **Step 2: Run tests to verify red**

Run: `go test ./internal/inventory ./internal/jobs -run 'Sync|Worker' -v`
Expected: FAIL because synchronization and workers do not exist.

- [ ] **Step 3: Implement synchronization and worker lifecycle**

```go
type Handler interface {
	Handle(context.Context, Job) error
}

type WorkerOptions struct {
	Owner string
	PollInterval time.Duration
	LeaseDuration time.Duration
	Concurrency int
}
```

Collect all provider pages with a configurable maximum of 10,000 servers, then call one transactional `ApplyCompleteSync`. Classify authentication and permission errors as terminal; rate limits, network errors, and 5xx-equivalent provider errors retry with bounded exponential delay.

- [ ] **Step 4: Compose and verify**

Start workers only after migrations and service construction. Stop accepting work on context cancellation, wait for active handlers within the existing shutdown budget, then close the database.

Run: `go test ./internal/inventory ./internal/jobs ./internal/app -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/inventory internal/jobs internal/app
git commit -m "feat: synchronize providers through persistent jobs"
```

---

### Task 6: Unified Inventory REST API

**Files:**
- Create: `internal/inventory/http.go`
- Create: `internal/inventory/http_test.go`
- Modify: `internal/app/app.go`

**Interfaces:**
- Produces: `GET /api/v1/servers`, `GET /api/v1/servers/{id}`, and `POST /api/v1/servers/{id}/refresh`.

- [ ] **Step 1: Write failing API tests**

```go
func TestListServersFiltersLocalInventory(t *testing.T) {
	handler := newInventoryHandlerFixture(t)
	request := authenticatedRequest(http.MethodGet, "/servers?state=running&connection_id=connection-a&q=api")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	require.JSONEq(t, `{"servers":[{"id":"server-a","name":"api-01","state":"running"}],"total":1}`, response.Body.String())
}
```

Cover state, provider type, connection, and text filters; stable name sorting; hidden rows excluded; 404 detail; and refresh queuing a connection synchronization job.

- [ ] **Step 2: Run tests to verify red**

Run: `go test ./internal/inventory -run HTTP -v`
Expected: FAIL because the handler does not exist.

- [ ] **Step 3: Implement strict query parsing and response models**

Return server identity, connection summary, scope, normalized and remote states, specifications, addresses, capabilities with unavailable reasons, portal availability, and last-seen timestamps. Never return provider credentials or raw console targets.

- [ ] **Step 4: Verify and commit**

Run: `go test ./internal/inventory ./internal/httpapi -v`
Expected: PASS.

```bash
git add internal/inventory internal/app
git commit -m "feat: expose unified local server inventory"
```

---

### Task 7: Connection and Server React Interface

**Files:**
- Create: `web/src/connections/types.ts`
- Create: `web/src/connections/api.ts`
- Create: `web/src/connections/ConnectionsPage.tsx`
- Create: `web/src/connections/ConnectionForm.tsx`
- Create: `web/src/connections/ConnectionsPage.test.tsx`
- Create: `web/src/servers/types.ts`
- Create: `web/src/servers/api.ts`
- Create: `web/src/servers/ServersPage.tsx`
- Create: `web/src/servers/ServerDetail.tsx`
- Create: `web/src/servers/ServersPage.test.tsx`
- Create: `web/src/dashboard/DashboardPage.tsx`
- Modify: `web/src/app/AppShell.tsx`
- Modify: `web/src/app/App.test.tsx`
- Modify: `web/src/styles.css`

**Interfaces:**
- Consumes: provider types, connection CRUD/test/sync, and inventory REST APIs.
- Produces: responsive total overview, provider connection management, server list, and server detail navigation.

- [ ] **Step 1: Write failing connection page tests**

```tsx
it('creates two independent Mock connections without showing credentials', async () => {
  render(<ConnectionsPage />)
  await user.click(await screen.findByRole('button', { name: '添加服务商' }))
  await user.type(screen.getByLabelText('连接名称'), '实验室 A')
  await user.type(screen.getByLabelText('服务器数量'), '4')
  await user.click(screen.getByRole('button', { name: '保存并同步' }))
  expect(await screen.findByText('实验室 A')).toBeInTheDocument()
  expect(screen.queryByText('write-only-token')).not.toBeInTheDocument()
})
```

Cover connection health badges, test button results, manual synchronization, disabled state, destructive-delete confirmation, empty state, loading, and API failures.

- [ ] **Step 2: Write failing inventory page tests**

Test connection/provider/state filters, text search, capability badges, server detail drawer, addresses, last synchronization time, and empty/error states. Power and console buttons render disabled with `里程碑 3` and `里程碑 4` explanatory labels rather than issuing unsupported calls.

- [ ] **Step 3: Run tests to verify red**

Run: `npm --prefix web test -- --run`
Expected: FAIL because the feature components do not exist.

- [ ] **Step 4: Implement typed APIs and pages**

Use the existing `apiRequest` client. Add in-memory navigation state for `总览`, `服务器`, and `服务商`; keep browser history and direct URL routing for `/`, `/servers`, `/servers/:id`, and `/connections` using a small `popstate` hook without adding a routing dependency.

- [ ] **Step 5: Complete the responsive visual states**

Keep the established dark operations-console design, accent color, focus indicators, and mobile sidebar. Add data tables that collapse into cards below 800 px, health/state chips that include text in addition to color, inline field errors, skeleton loading, and confirmation dialogs with accessible names.

- [ ] **Step 6: Verify frontend and production embedding**

Run:

```bash
npm --prefix web test -- --run
npm --prefix web run lint
npm --prefix web run build
./scripts/build.sh
go test ./...
```

Expected: all tests and lint pass; the single binary contains the current frontend.

- [ ] **Step 7: Commit**

```bash
git add web internal/webassets/dist
git commit -m "feat: add provider and unified inventory interface"
```

---

### Task 8: Milestone Verification and Operations Documentation

**Files:**
- Modify: `README.md`
- Modify: `compose.yaml`
- Modify: `compose.sqlite.yaml`
- Modify: `.env.example`
- Create: `docs/providers/mock.md`

**Interfaces:**
- Produces: reproducible SQLite/MySQL deployment and Mock demonstration instructions.

- [ ] **Step 1: Document exact key generation and Mock behavior**

Document `openssl rand -base64 32`, key rotation syntax, backup responsibility for the external key, Mock settings and failure modes, initial synchronization, and the fact that power/console workflows land in later milestones.

- [ ] **Step 2: Verify both deployment profiles**

Run the full Go/frontend suites, build the production image, start SQLite and MySQL profiles separately, initialize an administrator, create two Mock connections, request synchronization, and assert the combined server count from `/api/v1/servers`.

- [ ] **Step 3: Verify secret non-disclosure**

Search API fixtures, logs, rendered HTML, and database public query paths for the submitted Mock token. Confirm it appears only after test-side decryption and never in responses or logs.

- [ ] **Step 4: Commit**

```bash
git add README.md compose.yaml compose.sqlite.yaml .env.example docs/providers/mock.md
git commit -m "docs: document mock provider milestone operations"
```
