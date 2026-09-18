# GCP and Virtualizor Provider Design

**Date:** 2026-09-19

**Status:** Approved

## Goal

Add Google Cloud Compute Engine and Virtualizor Enduser API connections to the existing single-user server control panel. Each connection must remain independently configurable and encrypted, contribute servers to the unified inventory, support start/stop/reboot, and provide the safest available console or provider-portal fallback.

## Scope

This stage includes:

- Multiple independent GCP connections using one service-account JSON credential and one explicit project ID per connection.
- Multiple independent Virtualizor customer connections using an Enduser API key and API password.
- Inventory, instance lookup, start, stop, reboot, provider links, capability mapping, normalized errors, connection testing, and background synchronization.
- GCP provider-page fallback for console access.
- Virtualizor VNC through the existing authenticated, one-use local WebSocket session, including embedded and new-window noVNC modes.
- Provider-specific connection forms, documentation, protocol-faithful fixtures, production builds, and secret scanning.

This stage does not include GCP Application Default Credentials, Workload Identity Federation, user OAuth, Virtualizor Admin API, VM creation/deletion, snapshots, billing, networking changes, or live power tests without credentials and separate operator intent.

## Chosen approach

Use the official generated Compute Engine REST client (`google.golang.org/api/compute/v1`) with `option.WithCredentialsJSON` and the Compute scope, plus a custom bounded HTTPS client for Virtualizor Enduser API. The GCP client is constructed per connection and never uses Application Default Credentials.

Alternatives rejected:

1. Handwritten GCP REST and OAuth would reduce SDK dependencies but require custom JWT exchange, access-token caching, refresh, and compatibility handling.
2. Invoking `gcloud` or the Virtualizor PHP SDK would add external runtimes, subprocess failure modes, and opportunities to inherit host credentials.

## Architecture

### GCP adapter

`internal/providers/gcp` implements the existing `providers.Factory` and `providers.Provider` interfaces. The production factory creates a Compute Engine client from only the credential JSON stored on that connection. It must never load environment credentials, metadata-server credentials, shared files, or another connection's client.

The settings and credential payloads are:

```json
{"project_id":"example-project"}
```

```json
{"service_account_json": {"type":"service_account","project_id":"example-project"}}
```

The stored `service_account_json` value is the complete JSON object supplied by the user, not a filesystem path. Validation requires a service-account credential type, client email, private key, token URI, and an explicit project ID matching Google Cloud's 6–30 character lowercase project-ID syntax. The configured project may differ from the credential's project when that service account has cross-project IAM access.

Inventory uses Compute Engine's aggregated instance listing so a connection does not need a manually maintained zone list. Pagination tokens remain opaque inside the normalized cursor. Each server uses its instance name as `ExternalID` and its zone name as `Scope`, matching the identifiers required by instance lookup and power calls. The immutable numeric instance ID is retained only as non-secret display/spec metadata. The adapter maps name, status, machine type, labels, internal/external addresses, and relevant scheduling attributes without returning the credential.

GCP state mapping is:

- `PROVISIONING` and `STAGING` → `pending`
- `RUNNING` → `running`
- `STOPPING` and `SUSPENDING` → `stopping`
- `SUSPENDED` → `suspended`
- `TERMINATED` → `stopped`
- `REPAIRING` or an unrecognized value → `unknown`

Start and stop call the corresponding Compute Engine methods. The panel's reboot action calls Compute Engine `reset`, which is a hard reset; UI confirmation text and documentation must say this for GCP. The returned operation name becomes the normalized request ID.

The provider portal is the HTTPS Google Cloud Console page for the configured project, zone, and instance. Embedded and separate-window serial-console capabilities are unavailable because browser serial access depends on a Google Console session and additional instance/IAM configuration. The resource page remains available as fallback.

### Virtualizor adapter

`internal/providers/virtualizor` implements the same Provider interfaces using the customer-facing Enduser API, normally rooted at `https://hostname:4083/`. It accepts a panel base URL and stores these credentials:

```json
{"api_key":"...","api_password":"..."}
```

The adapter uses `index.php`, `api=json`, `apikey`, and `apipass` as required by the official API. Credentials may exist in the upstream request query because Virtualizor requires it, but URLs containing those values must never be logged, audited, included in error messages, stored in inventory, or returned to the browser.

Connection validation calls `act=listvs` and accepts an account with zero VPSs. Inventory parses the top-level numeric VPS-ID entries while ignoring page metadata. It must tolerate documented Virtualizor responses that encode numbers and booleans as either JSON numbers or strings. A malformed VPS entry fails the complete synchronization rather than silently producing partial inventory.

Virtualizor state mapping gives suspension precedence, then maps status `1` to `running`, `0` to `stopped`, `2` to `suspended`, and unknown values to `unknown`. Server data includes hostname/name, virtualization type, CPU cores, RAM, storage, bandwidth, server/location label when exposed, and IPv4/IPv6 addresses. Start, stop, and restart call the documented Enduser actions with `do=1` and the selected VPS ID. A response succeeds only when it contains the documented success marker rather than merely returning HTTP 200.

The portal fallback is the same validated panel origin with `index.php?act=vpsmanage&svs=<id>`. It never contains API credentials.

### Shared provider network policy

The endpoint/DNS restrictions currently owned by the VirtFusion adapter become a small shared internal provider-network package. VirtFusion behavior must remain unchanged after extraction.

The policy:

- Requires HTTPS for production provider endpoints.
- Rejects URL user information, fragments, protocol downgrade, cross-origin redirects, loopback, unspecified, multicast, and link-local destinations. Redirects are followed only when both scheme and origin remain unchanged.
- Rejects private addresses unless every resolved address falls within `PROVIDER_ALLOWED_PRIVATE_CIDRS`.
- Resolves and checks destinations when saving a connection, following redirects, and opening each TCP connection to mitigate DNS rebinding.
- Does not support global TLS verification disablement. A future custom-CA feature may be added separately.

### Raw TCP VNC gateway

The current console flow remains the trust boundary: the authenticated API obtains a provider target, validates it, creates a short-lived one-use ticket, and stores the upstream target only in memory. The browser receives the local ticket, console protocol, and temporary VNC password, but never the upstream IP or port.

Virtualizor VNC Info returns a raw VNC IP, port, password, and availability flag. The adapter emits an internal `vnc+tcp://host:port` target only when the VPS and response are valid; this internal-only scheme is never returned to the browser. The central console target policy recognizes that scheme only for Virtualizor console targets and validates the hostname/IP and port against the same public/private restrictions before a ticket is created.

The WebSocket gateway supports three transports:

- Mock terminal WebSocket.
- VirtFusion upstream WSS proxy.
- Virtualizor raw TCP VNC proxy.

For raw VNC, downstream WebSocket binary messages are written as bytes to the TCP connection, and TCP read chunks are returned as binary WebSocket messages. Text messages, oversized frames, blocked destinations, expired tickets, and unavailable targets fail closed. The gateway retains existing idle and absolute timeouts. VNC frames and clipboard data never enter React state, application logs, audit metadata, or persistence.

Both embedded and new-window modes use the existing noVNC component and same-origin `postMessage` handoff. The temporary password remains in memory and is not placed in URLs, local storage, or session storage.

## Provider-specific UI

The connection modal adds:

- GCP: connection name, project ID, and a multiline service-account JSON textarea. The browser parses it into the `service_account_json` object before submission; the secret is write-only after submission.
- Virtualizor: connection name, HTTPS panel URL, API key, and API password. Both credential fields are write-only.

The UI permits any number of connections of the same type. Provider-specific validation hints explain required formats and the private-CIDR setting. Saved connection cards and API responses continue to omit credentials.

The GCP reboot confirmation explicitly says that Compute Engine performs a hard reset. Virtualizor exposes embedded/new-window VNC only when VNC information is available; otherwise the provider portal remains the console fallback.

## Error handling

GCP authentication, IAM permission, quota/rate-limit, missing instance, conflict/precondition, timeout, and generic service errors map to the existing normalized provider error codes. No error may include credential JSON, private-key fragments, OAuth tokens, or raw response bodies.

Virtualizor classifies HTTP authentication/permission/not-found/rate-limit/server errors and also inspects API-level error/success fields in HTTP-200 responses. Malformed JSON, oversized responses, incomplete pagination/inventory, invalid VNC coordinates, timeout, and redirect-policy failures produce safe normalized errors. Errors must not contain API keys, passwords, credential-bearing request URLs, VNC passwords, or untrusted response bodies.

## Testing strategy

Implementation follows red-green-refactor TDD.

- GCP unit tests inject a fake Compute client and cover aggregated pagination, multiple zones, mapping, get, actions, operation IDs, portal URLs, errors, malformed credentials, and isolation between two connections.
- Virtualizor uses local protocol-faithful HTTPS fixtures for empty and populated accounts, mixed JSON types, actions, API-level failures, VNC details, cross-origin redirects, unsafe destinations, and isolation among multiple connections.
- Shared network-policy tests cover public, explicitly allowed private, loopback, link-local, mixed DNS answers, rebinding, redirect downgrade, and cross-origin redirect cases.
- Console gateway tests cover raw TCP bidirectional binary forwarding, text rejection, one-use tickets, target cleanup, idle closure, and failure results.
- Frontend tests cover both write-only forms, GCP hard-reset warning, noVNC selection, embedded/new-window sessions, fallback messages, and absence of secrets from rendered content and URLs.
- Final verification runs all Go tests, frontend tests, lint, TypeScript/production build, embedded-asset synchronization, and Docker build. It scans responses, assets, logs, and test databases for submitted test secrets and confirms that temporary fixtures, containers, images, volumes, and listeners are stopped.

Live smoke tests are optional and require user-supplied credentials. They begin with read-only connection validation and inventory. No live power action runs without a separate explicit request identifying the target VM.

## Documentation and operational requirements

Provider documentation lists minimum GCP IAM permissions for instance discovery, start, stop, reset, and get operations; explains the hard-reset behavior and console fallback; and warns that long-lived service-account keys should be narrowly scoped and protected. Virtualizor documentation covers Enduser API credential creation, HTTPS requirements, private CIDR configuration, VNC prerequisites, and the fact that upstream credentials are necessarily sent as query parameters to the provider but never exposed by this panel.

Official protocol references:

- Google Compute Engine REST API: <https://docs.cloud.google.com/compute/docs/reference/rest/v1>
- Google service-account credentials: <https://docs.cloud.google.com/iam/docs/service-account-creds>
- Virtualizor Enduser API: <https://www.virtualizor.com/docs/enduser-api/>
- Virtualizor List VPS: <https://www.virtualizor.com/docs/enduser-api/list-vps/>
- Virtualizor VNC Info: <https://www.virtualizor.com/docs/enduser-api/enduser-vnc-info/>

## Acceptance criteria

1. Two GCP connections with different JSON credentials and projects cannot share clients, tokens, inventory, or actions.
2. Three Virtualizor connections with different endpoints and credentials cannot share requests, inventory, or VNC targets.
3. Unified inventory and start/stop/reboot work through the existing job, operation, audit, and event flows without schema changes.
4. GCP console requests fall back to a credential-free official provider URL.
5. Virtualizor VNC works through a one-use local ticket in embedded and new-window modes without exposing its upstream target.
6. Unsafe endpoints and VNC destinations fail closed; existing VirtFusion endpoint behavior does not regress.
7. Credentials and temporary console secrets are absent from connection responses, inventory, errors, audit entries, URLs produced for browsers, logs, databases outside the encrypted envelope, and built assets.
8. Full backend/frontend verification and the production Docker build pass with no temporary server left running.
