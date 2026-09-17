# Foundation, Dual Database, and Administrator Authentication Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a runnable Go/React application that starts with SQLite or MySQL, initializes exactly one administrator by setup wizard or environment variables, authenticates with database-backed sessions and CSRF protection, and embeds the production frontend in one executable.

**Architecture:** A Go modular monolith exposes Chi JSON APIs and owns configuration, persistence, authentication, sessions, and static delivery. React/TypeScript supplies setup, login, and the authenticated shell. Dialect-specific SQL stays behind one repository contract tested against both databases.

**Tech Stack:** Go, Chi v5, `database/sql`, modernc SQLite, go-sql-driver/mysql, Argon2id, React, TypeScript, Vite, Vitest, Testing Library, Docker Compose.

**Spec:** `docs/superpowers/specs/2026-09-17-server-control-panel-design.md`

## Global Constraints

- Exactly one local administrator; no registration, tenant, organization, or RBAC.
- `DATABASE_URL` selects SQLite or MySQL; never dual-write.
- SQLite enables WAL, foreign keys, and busy timeout and allows one application instance.
- Both databases satisfy the same repository contract and migration version.
- Passwords use Argon2id; session and CSRF secrets are stored only as hashes.
- Successful initialization permanently closes both initialization paths.
- Session cookies are `HttpOnly`, `SameSite=Lax`, and `Secure` in production.
- Mutations require same-origin and CSRF validation.
- Production React assets use `go:embed`.
- Secrets never enter logs or error details.

## File Map

```text
cmd/controlpanel/main.go       process entrypoint
internal/app/                 composition and lifecycle
internal/config/              typed environment configuration
internal/database/            drivers and migrations
internal/auth/                passwords, repository, service, HTTP
internal/httpapi/             router, errors, health
internal/webassets/           embedded frontend
web/src/api/                  JSON client
web/src/auth/                 setup and login
web/src/app/                  authenticated shell
scripts/build.sh              frontend-to-embed build
Dockerfile                    production image
compose*.yaml                 MySQL and SQLite deployment
```

---

### Task 1: Runnable Go Server and Embedded Frontend Contract

**Files:**
- Create: `go.mod`, `cmd/controlpanel/main.go`
- Create: `internal/app/app.go`
- Create: `internal/httpapi/router.go`, `internal/httpapi/router_test.go`
- Create: `internal/webassets/embed.go`, `internal/webassets/dist/.gitkeep`

**Interfaces:**
- Produces: `httpapi.NewRouter(httpapi.Dependencies) http.Handler`
- Produces: `app.Run(context.Context, string, http.Handler) error`; Task 2 replaces the address argument with typed `config.Config` during composition.
- Produces: `webassets.FileSystem() fs.FS`

- [ ] **Step 1: Initialize dependencies**

```bash
go mod init controlpanel
go get github.com/go-chi/chi/v5
go get github.com/stretchr/testify
```

- [ ] **Step 2: Write failing router tests**

```go
func TestHealthLive(t *testing.T) {
	r := NewRouter(Dependencies{Assets: fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("app-shell")},
	}})
	res := httptest.NewRecorder()
	r.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/health/live", nil))
	require.Equal(t, 200, res.Code)
	require.JSONEq(t, `{"status":"ok"}`, res.Body.String())
}

func TestFrontendFallback(t *testing.T) {
	r := NewRouter(Dependencies{Assets: fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("app-shell")},
	}})
	res := httptest.NewRecorder()
	r.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/servers/demo", nil))
	require.Equal(t, "app-shell", res.Body.String())
}
```

- [ ] **Step 3: Confirm red, implement, confirm green**

Run: `go test ./internal/httpapi -v`  
Expected before implementation: FAIL because router types are absent.

Implement:

```go
type Dependencies struct { Assets fs.FS }
func NewRouter(deps Dependencies) http.Handler

//go:embed dist/*
var assets embed.FS
func FileSystem() fs.FS

func Run(ctx context.Context, address string, handler http.Handler) error
```

Serve `/health/live`, reserve `/api/v1`, serve files, and use `index.html` only for extensionless GET paths outside `/api`, `/health`, and `/ws`. Add graceful shutdown and HTTP timeouts.

- [ ] **Step 4: Verify and commit**

```bash
go test ./...
git add go.mod go.sum cmd internal
git commit -m "feat: bootstrap embedded Go web server"
```

---

### Task 2: Configuration and Dialect-Aware Database Foundation

**Files:**
- Create: `internal/config/config.go`, `internal/config/config_test.go`
- Create: `internal/database/database.go`, `internal/database/migrate.go`, `internal/database/migrations.go`
- Create: `internal/database/database_test.go`, `internal/database/mysql_test.go`
- Modify: `internal/app/app.go`, `internal/httpapi/router.go`, `cmd/controlpanel/main.go`

**Interfaces:**
- Produces: `config.Load() (config.Config, error)`
- Produces: `database.Open(ctx, config.DatabaseConfig) (*sql.DB, database.Dialect, error)`
- Produces: `database.Migrate(ctx, db, dialect) error`

- [ ] **Step 1: Write failing configuration tests**

```go
func TestLoadDefaultsToSQLite(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, "sqlite://data/controlpanel.db", cfg.Database.URL)
}

func TestProductionRequiresHTTPS(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("PUBLIC_ORIGIN", "http://panel.example.test")
	_, err := Load()
	require.ErrorContains(t, err, "https")
}
```

Also cover malformed database URLs, partial bootstrap credentials, and valid production configuration.

- [ ] **Step 2: Confirm red and implement configuration**

Run: `go test ./internal/config -v`  
Expected: FAIL because the package is absent.

Implement:

```go
type Config struct {
	Environment string
	HTTP HTTPConfig
	Database DatabaseConfig
	Auth AuthBootstrapConfig
}
type DatabaseConfig struct { URL string }
type HTTPConfig struct { Address string; PublicOrigin *url.URL }
type AuthBootstrapConfig struct { Username, Password string }
```

Reject partial bootstrap configuration and never stringify its password.

- [ ] **Step 3: Write failing migration tests**

```go
func TestSQLiteMigrationsAreIdempotent(t *testing.T) {
	db, dialect := openSQLiteTestDB(t)
	require.NoError(t, Migrate(context.Background(), db, dialect))
	require.NoError(t, Migrate(context.Background(), db, dialect))
	requireTableExists(t, db, "users")
	requireTableExists(t, db, "sessions")
}
```

Also assert foreign keys and the single-user database constraint.

- [ ] **Step 4: Confirm red and implement database support**

```bash
go test ./internal/database -v
go get modernc.org/sqlite
go get github.com/go-sql-driver/mysql
```

Expected first run: FAIL. Implement `DialectSQLite`/`DialectMySQL`, DSN parsing without logging secrets, SQLite WAL/foreign keys/5-second busy timeout, and version 1 `schema_migrations`, `users`, `sessions` migrations. Use a singleton key fixed to `1`; store token hashes as 32 bytes.

- [ ] **Step 5: Add MySQL contract and readiness**

MySQL tests skip unless `TEST_MYSQL_DSN` is set, create a uniquely named temporary database, run the same assertions, and safely drop only that database. Add:

```go
type Readiness interface { PingContext(context.Context) error }
```

`/health/ready` succeeds only after migration and ping. Migrate before listening and close the DB on shutdown.

- [ ] **Step 6: Verify and commit**

```bash
go test ./...
git add go.mod go.sum cmd internal
git commit -m "feat: add SQLite and MySQL database foundation"
```

---

### Task 3: Passwords, Repository, and Authentication Service

**Files:**
- Create: `internal/auth/model.go`, `internal/auth/password.go`, `internal/auth/password_test.go`
- Create: `internal/auth/repository.go`, `internal/auth/sql_repository.go`, `internal/auth/repository_contract_test.go`
- Create: `internal/auth/service.go`, `internal/auth/service_test.go`

**Interfaces:**
- Produces: `PasswordHasher`, `Repository`, and `Service`.
- Produces: `SetupStatus`, `Initialize`, `Login`, `Authenticate`, `Logout`, `ChangePassword`.
- Consumes: Task 2 schema.

- [ ] **Step 1: Write failing Argon2id tests**

```go
func TestPasswordRoundTrip(t *testing.T) {
	h := NewArgon2idHasher(DefaultArgon2idParams())
	encoded, err := h.Hash("correct horse battery staple")
	require.NoError(t, err)
	ok, err := h.Verify(encoded, "correct horse battery staple")
	require.NoError(t, err)
	require.True(t, ok)
}
```

Add malformed PHC, wrong password, salt uniqueness, and 1,024-byte limit cases.

- [ ] **Step 2: Confirm red and implement password hashing**

Run: `go test ./internal/auth -run Password -v`  
Expected: FAIL. Install `golang.org/x/crypto/argon2`; use 16 random salt bytes, 32 output bytes, strict parsing, 1 KiB encoded limit, and constant-time comparison.

- [ ] **Step 3: Write failing repository contracts**

```go
type User struct {
	ID, Username, PasswordHash string
	InitializedAt time.Time
}
type Session struct {
	TokenHash, CSRFHash [32]byte
	UserID string
	ExpiresAt, LastSeenAt time.Time
}
var ErrAlreadyInitialized = errors.New("administrator already initialized")
var ErrNotFound = errors.New("not found")
```

Test atomic single-user initialization, normalized lookup, session create/read/touch/delete, expiry, and revoke-all-except-current against SQLite and opt-in MySQL.

- [ ] **Step 4: Confirm red and implement repository**

Run: `go test ./internal/auth -run RepositoryContract -v`  
Expected: FAIL. Implement bound SQL queries and duplicate-key translation. Store normalized username separately from display username.

- [ ] **Step 5: Write failing service tests**

Use fake repository, clock, and random source. Cover both initialization paths through the same method, duplicate initialization, generic login failure, hashes-only persistence, expired session deletion, and password-change revocation.

```go
type SessionGrant struct {
	SessionToken, CSRFToken string
	ExpiresAt time.Time
	User User
}
```

- [ ] **Step 6: Confirm red and implement service**

Run: `go test ./internal/auth -run Service -v`  
Expected: FAIL. Generate independent 32-byte tokens, return unpadded base64url, store SHA-256, apply 12-hour idle and seven-day absolute limits, and throttle session touches.

- [ ] **Step 7: Verify and commit**

```bash
go test ./internal/auth -v
git add go.mod go.sum internal/auth
git commit -m "feat: add single-administrator authentication service"
```

---

### Task 4: Secure Authentication HTTP API

**Files:**
- Create: `internal/auth/http.go`, `internal/auth/http_test.go`
- Create: `internal/httpapi/errors.go`
- Modify: `internal/httpapi/router.go`, `internal/app/app.go`, `cmd/controlpanel/main.go`

**Interfaces:**
- Produces: setup status/initialize and auth login/logout/me/password endpoints.
- Produces: session, origin, and CSRF middleware.
- Produces: stable `{error:{code,message,request_id}}` envelope.

- [ ] **Step 1: Write failing HTTP security tests**

```go
func TestLoginSetsSecureCookie(t *testing.T) {
	h := newAuthHandlerForTest(t, "production", "https://panel.example.test")
	req := jsonRequest(t, "POST", "/api/v1/auth/login", validLogin)
	req.Header.Set("Origin", "https://panel.example.test")
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	require.Equal(t, 200, res.Code)
	require.Contains(t, res.Header().Get("Set-Cookie"), "HttpOnly")
	require.Contains(t, res.Header().Get("Set-Cookie"), "Secure")
}
```

Cover closed setup, malformed/oversized JSON, generic login error, expired session, wrong origin, bad CSRF, logout clearing, and password-change revocation.

- [ ] **Step 2: Confirm red and implement stable errors**

Run: `go test ./internal/auth -run HTTP -v`  
Expected: FAIL.

```go
type APIError struct {
	Code string `json:"code"`
	Message string `json:"message"`
	RequestID string `json:"request_id"`
}
```

Generate request IDs and never return raw internal errors.

- [ ] **Step 3: Implement handlers and middleware**

Limit JSON to 64 KiB and reject unknown fields. Require exact configured `Origin`; reject cross-site `Sec-Fetch-Site` when Origin is absent. Validate SHA-256 of `X-CSRF-Token`. Cookie: `controlpanel_session`, path `/`, `HttpOnly`, `SameSite=Lax`, production `Secure`.

Mount:

```text
GET  /api/v1/setup/status
POST /api/v1/setup/initialize
POST /api/v1/auth/login
POST /api/v1/auth/logout
GET  /api/v1/auth/me
PUT  /api/v1/auth/password
```

`/auth/me` returns the user and a fresh in-memory CSRF value. Before listening, complete bootstrap credentials call the same `Initialize`; already initialized is success.

- [ ] **Step 4: Verify and commit**

```bash
go test ./...
git add cmd internal
git commit -m "feat: expose secure setup and authentication API"
```

---

### Task 5: React Setup, Login, and Authenticated Shell

**Files:**
- Create: Vite React/TypeScript files under `web/`
- Create: `web/src/api/client.ts` and test
- Create: `web/src/auth/AuthContext.tsx`, setup/login pages and tests
- Create: `web/src/app/App.tsx`, `AppShell.tsx`, tests, and `web/src/styles.css`

**Interfaces:**
- Produces: `apiRequest<T>`, `setCSRFToken`, `AuthProvider`, `useAuth`.
- Consumes: Task 4 API.

- [ ] **Step 1: Scaffold frontend and tests**

```bash
npm create vite@latest web -- --template react-ts
cd web
npm install
npm install -D vitest jsdom @testing-library/react @testing-library/user-event @testing-library/jest-dom
```

Remove demos; configure jsdom and proxy `/api` and `/ws` to port 8080.

- [ ] **Step 2: Write client tests, confirm red, implement**

Test credentials inclusion, JSON, mutation-only CSRF, stable error parsing, and no web-storage writes.

```ts
export type APIError = { code: string; message: string; request_id: string }
export function setCSRFToken(token: string | null): void
export async function apiRequest<T>(path: string, init?: RequestInit): Promise<T>
```

Run: `npm test -- --run src/api/client.test.ts`; fail before implementation, pass after.

- [ ] **Step 3: Write auth-state tests, confirm red, implement**

```ts
type AuthState =
  | { status: "loading" }
  | { status: "setup-required" }
  | { status: "unauthenticated" }
  | { status: "authenticated"; user: { id: string; username: string } };
```

Cover startup, initialization, login, logout, and reload via `/auth/me`. Keep CSRF only in memory.

- [ ] **Step 4: Write view tests, confirm red, implement**

Test confirmation matching, pending states, generic errors, auth-state routing, and navigation labels 总览、服务器、服务商、操作记录、备份与设置. Use semantic forms, `aria-live`, autocomplete, keyboard focus, and a mobile navigation toggle. Dark-first tokens must avoid horizontal overflow at 360 px. Do not add provider/server fake data.

- [ ] **Step 5: Verify and commit**

```bash
npm test -- --run
npm run build
npm run lint
git add web
git commit -m "feat: add setup login and operator shell"
```

---

### Task 6: Embedded Build, Docker Packaging, and Verification

**Files:**
- Create: `scripts/build.sh`, `Makefile`, `Dockerfile`
- Create: `compose.yaml`, `compose.sqlite.yaml`
- Create: `.dockerignore`, `.env.example`, `.gitignore`, `README.md`
- Modify: `internal/httpapi/router_test.go`, `internal/webassets/dist/`

**Interfaces:**
- Produces: `make test`, `make build`, `make integration-mysql`.
- Produces: `dist/controlpanel`, MySQL Compose, SQLite override.

- [ ] **Step 1: Write failing embedded-asset test**

Assert embedded `index.html` exists and references a hashed `/assets/` file.

Run: `go test ./internal/httpapi -run EmbeddedAssets -v`  
Expected: FAIL while only `.gitkeep` exists.

- [ ] **Step 2: Implement repeatable build**

```bash
#!/usr/bin/env bash
set -euo pipefail
npm --prefix web ci
npm --prefix web test -- --run
npm --prefix web run build
find internal/webassets/dist -mindepth 1 ! -name .gitkeep -delete
cp -R web/dist/. internal/webassets/dist/
go test ./...
mkdir -p dist
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o dist/controlpanel ./cmd/controlpanel
```

Validate the resolved deletion target first. Add Make targets and `--version` behavior.

- [ ] **Step 3: Add container and deployment files**

Use Node and Go build stages and a non-root minimal runtime. MySQL has health checks. SQLite override removes MySQL dependency, sets an explicit SQLite URL, and mounts `/data`. Do not hard-code secrets. Document setup, bootstrap, databases, HTTPS, health checks, and application-level cross-database migration.

- [ ] **Step 4: Verify SQLite runtime**

Start on port 18080 with an explicit temporary SQLite directory, then:

```bash
curl --fail http://127.0.0.1:18080/health/live
curl --fail http://127.0.0.1:18080/health/ready
curl --fail http://127.0.0.1:18080/api/v1/setup/status
```

Expected: health 200, setup required, database retained after clean stop.

- [ ] **Step 5: Verify MySQL and Compose**

```bash
docker compose config
docker compose -f compose.yaml -f compose.sqlite.yaml config
docker compose up -d --build
go test ./internal/database ./internal/auth -run 'MySQL|RepositoryContract' -v
docker compose down
```

Expected: app ready and contracts PASS; do not remove volumes.

- [ ] **Step 6: Full verification and commit**

```bash
go test ./...
npm --prefix web test -- --run
npm --prefix web run build
npm --prefix web run lint
make build
git status --short
git add .dockerignore .env.example .gitignore Dockerfile Makefile README.md compose.yaml compose.sqlite.yaml scripts internal/webassets
git commit -m "build: package foundation as single binary and container"
```

## Completion Criteria

- A clean checkout builds one executable with the React frontend.
- SQLite runs alone; MySQL runs through Compose; both pass equivalent contracts.
- Setup wizard or complete environment credentials initializes exactly one administrator.
- Login stores hashed sessions and enforces Cookie, origin, and CSRF controls.
- Refresh restores auth without exposing the session token to JavaScript.
- Password change revokes other sessions.
- Go, frontend, embedded-asset, SQLite, and MySQL tests pass.
- Provider, inventory, operations, console, and backup code remains deferred.
