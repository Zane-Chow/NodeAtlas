# Console Fallback and Encrypted Backup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Keep the approved design boundaries intact.

**Goal:** Complete stage-one milestone 4 with capability-driven embedded/new-window/provider-portal console access and portable encrypted backups that can be validated and restored on SQLite or MySQL.

**Architecture:** A `console` domain resolves provider capabilities, validates every externally supplied target, stores only hashed one-use tickets and lifecycle metadata, and keeps temporary targets in process memory until the WebSocket consumes them. A `backup` domain exports an explicit, versioned logical schema, authenticates and encrypts it with a user-provided passphrase, stores only non-sensitive file metadata in the database, and restores in one transaction after a safety snapshot and full validation. Both domains are mounted behind the existing single-user session, origin and CSRF middleware.

**Tech Stack:** Go 1.27, Chi v5, `database/sql`, AES-256-GCM, scrypt, coder/websocket, SQLite/MySQL, React 19, TypeScript 6, Vitest.

**Spec:** `docs/superpowers/specs/2026-09-17-server-control-panel-design.md` sections 8.7, 9, 11, 14, 15, 16.4, 16.5, 19 and stage-one milestone 4.

## Global constraints

- Never return a provider console target, credential or temporary token for embedded mode to the browser.
- One-use console tickets expire after 60 seconds, are invalidated during the WebSocket handshake, and only their SHA-256 hashes are persisted.
- External console and portal URLs are produced by the configured Provider, never accepted from the client, and must pass scheme, hostname, resolution and private-address policy checks.
- Mock embedded sessions use a registered in-process test transport; real adapters must use validated `wss` targets.
- Backup files use a versioned envelope with scrypt-derived AES-256-GCM, random salt and nonce, and a SHA-256 checksum over the encrypted envelope.
- Backup passphrases are request-only values: they never enter the database, filename, logs, audit metadata or API responses.
- Archive records are an explicit logical schema, not SQL text or a copied SQLite file. Restore must therefore work across SQLite and MySQL.
- A restore validates envelope, format version, record manifest and referential order before mutation, creates a safety snapshot first, and replaces data in one transaction.
- Console byte streams and terminal contents are never logged or persisted.

---

### Task 1: Persistence and Configuration

**Files:** `internal/database/migrations.go`, database tests, new console/backup models and repositories, `internal/config/config.go`.

- [x] Add migration v4 for `console_sessions`, `backups`, and `settings` in both dialects.
- [x] Add SQLite/MySQL-safe repositories and contract tests for one-use tickets and backup metadata.
- [x] Add `BACKUP_DIRECTORY` configuration with a safe default; create it when the backup service starts.
- [x] Verify migration idempotency and repository behavior.

### Task 2: Console Target Policy and Service

**Files:** new `internal/console` policy, service, memory target store and tests.

- [x] Test rejection of HTTP embedded targets, userinfo, fragments, loopback, link-local, multicast, unspecified, cloud metadata and unapproved private addresses.
- [x] Implement resolver-injected URL policy; allow `wss` for embedded and `https` for external pages.
- [x] Resolve options in embedded → new window → portal order from server capabilities.
- [x] Create hashed one-use tickets, keep temporary targets only in memory, and append sanitized audit entries.
- [x] Implement provider portal resolution with the same URL policy.

### Task 3: WebSocket Console Gateway

**Files:** new `internal/console/websocket.go`, router/app composition, Mock provider and tests.

- [ ] Add WebSocket handshake tests for missing, expired, reused and valid tickets.
- [ ] Consume tickets atomically before opening the target.
- [ ] Proxy binary/text frames bidirectionally for validated `wss` targets with idle and absolute deadlines.
- [ ] Register an in-process Mock console transport that emits a banner and echoes input without network access.
- [ ] Record opened/closed/result metadata only and clear in-memory target material.

### Task 4: Backup Envelope and Logical Snapshot

**Files:** new `internal/backup` crypto, schema, exporter/restorer and tests.

- [ ] Write round-trip, wrong-passphrase, tamper and manifest-validation tests.
- [ ] Implement versioned JSON logical records for users, connections (encrypted credentials included), servers, operations, jobs, audit logs, console sessions and settings.
- [ ] Normalize binary, boolean, nullable and timestamp fields across dialects.
- [ ] Encrypt with scrypt + AES-256-GCM and compute an envelope checksum.
- [ ] Restore by deleting child tables and inserting parents in explicit referential order inside one transaction.

### Task 5: Backup Files, Safety Snapshot and HTTP API

**Files:** backup service/HTTP handlers, app composition and tests.

- [ ] Create/list/download backup metadata without exposing filesystem paths or passphrases.
- [ ] Create the configured backup directory with restrictive permissions when the service starts.
- [ ] Validate an uploaded or stored archive without changing data.
- [ ] Before restore, create a safety snapshot using the supplied passphrase; reject restore while non-terminal work is active.
- [ ] Restore transactionally and append sanitized audit events for create/validate/restore outcomes.
- [ ] Enforce bounded request/upload sizes, safe generated filenames and attachment download headers.

### Task 6: Console React Experience

**Files:** server API/types/detail, new console client/component and tests.

- [ ] Replace the disabled console button with capability-driven options.
- [ ] Open embedded Mock console in a dialog using WebSocket and show connection state without logging frames.
- [ ] Open temporary console or provider portal links in a new tab using `noopener,noreferrer`.
- [ ] Display the exact fallback level and safe error messages.

### Task 7: Backup and Settings React Experience

**Files:** new backup page/API/types/tests, app navigation and CSS.

- [ ] Enable “备份与设置”, list backups and create one with a non-retained passphrase.
- [ ] Download, validate and restore with explicit destructive confirmation.
- [ ] Clear passphrase fields after every request and never store them in browser persistence.
- [ ] Add responsive states for empty, running, success and failure results.

### Task 8: Documentation and Milestone Verification

**Files:** README, `docs/console.md`, `docs/backups.md`, compose files and env example.

- [ ] Document target policy, proxy requirements, ticket lifetime, backup key ownership and recovery limits.
- [ ] Add a dedicated backup volume/directory to direct and Compose deployment examples.
- [ ] Run full Go/frontend tests, lint, production build and Docker image build.
- [ ] Exercise all four Mock console profiles and create/validate/restore a backup on SQLite.
- [ ] Restore the SQLite-created archive into MySQL and verify record counts and encrypted credentials.
- [ ] Search responses, logs, database values, archives and assets for submitted console/backup secrets; remove all temporary services and listeners.
