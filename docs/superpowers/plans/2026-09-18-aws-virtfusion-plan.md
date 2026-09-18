# AWS and VirtFusion Provider Implementation Plan

**Goal:** Complete stage two with multiple independent AWS and VirtFusion connections, real inventory and power APIs, safe console fallback, dynamic connection forms, and reusable Provider contract tests.

**Architecture:** Keep vendor SDK and HTTP shapes inside `internal/providers/aws` and `internal/providers/virtfusion`. Both adapters emit only the existing normalized Provider model. AWS uses explicit static credentials and a configured region list; it does not fall through to host credential discovery. VirtFusion uses a bounded HTTPS JSON client with Bearer authentication, pagination and endpoint address policy. Real console targets still pass the central console target policy before the browser receives a local ticket or validated external URL.

**Official references:** AWS SDK for Go v2 and EC2 API documentation; AWS EC2 Serial Console prerequisites and connection documentation; VirtFusion Global API OpenAPI specification at `https://docs.virtfusion.com/api/openapi.yaml` (API v1, including the VNC endpoint added in VirtFusion 6.1).

## Decisions and boundaries

- AWS credentials are `{access_key_id, secret_access_key, session_token?}` and settings contain one or more explicit regions. AssumeRole, shared files, environment credentials and instance roles are out of scope.
- AWS inventory is paginated across regions. The server scope is the AWS region and the external ID is the EC2 instance ID.
- AWS power actions use EC2 StartInstances, StopInstances and RebootInstances. The adapter maps AWS request IDs into the normalized receipt.
- AWS browser serial console cannot be safely generated from static API credentials alone: it also requires account enablement, IAM permission and an AWS browser session. Stage two advertises the AWS resource page as the provider-portal fallback and reports serial console unavailable with a precise reason.
- VirtFusion endpoint input is a control-panel base URL; requests use `/api/v1`, Bearer Token authentication, strict JSON limits and bounded timeouts. Redirects are revalidated and private destinations require an explicit provider CIDR allowlist.
- VirtFusion inventory uses pages of at most 200 servers and retrieves remote state/details as needed. It never treats partial pagination as a successful full sync.
- VirtFusion 6.1+ VNC details may contain a relative WSS URL and short-lived token. The adapter resolves it against the validated control origin; targets/passwords remain request-only and are never persisted or returned directly.
- Embedded VirtFusion VNC will use a noVNC client over the existing one-use local WebSocket gateway. The existing terminal-style Mock console remains available for its own transport.
- Multiple connections of either type remain isolated by connection ID, encrypted credential envelope and Provider factory instance.

## Task 1: Provider metadata and reusable contracts

- [ ] Extend provider type metadata with display name and a non-secret form descriptor without returning stored values.
- [ ] Add a reusable Provider contract suite covering pagination, mapping, actions, error classes, console fallback and secret-free results.
- [ ] Run the Mock adapter through the shared suite before adding real adapters.

## Task 2: AWS adapter

- [ ] Add AWS SDK for Go v2 EC2 dependencies with an injected EC2 client factory for tests.
- [ ] Validate and normalize AWS regions/settings and static credential JSON without exposing secrets in errors.
- [ ] Implement cross-region pagination, instance lookup, state/spec/address/capability mapping and name-tag selection.
- [ ] Implement start, stop and reboot with normalized request receipts and Smithy error classification.
- [ ] Generate partition-aware HTTPS EC2 resource links and explicit serial-console fallback reasons.
- [ ] Pass unit, shared contract and application composition tests with two independent AWS connections.

## Task 3: VirtFusion adapter and endpoint policy

- [ ] Add a provider API target policy and `PROVIDER_ALLOWED_PRIVATE_CIDRS` configuration for self-hosted control panels.
- [ ] Implement a bounded Bearer JSON client for `/connect`, paginated `/servers`, detailed server lookup and power actions.
- [ ] Map VirtFusion commissioned/suspended/remote states, resources, addresses and capabilities to the normalized model.
- [ ] Implement VNC detail retrieval, safe relative WSS resolution and control-panel portal fallback.
- [ ] Classify 401/403/404/409/422/429/5xx, malformed payload, timeout and pagination errors without leaking response secrets.
- [ ] Pass unit, shared contract and application composition tests with three independent VirtFusion connections.

## Task 4: noVNC console experience

- [ ] Add the maintained noVNC browser client and keep VNC frames out of React state, logs and persistence.
- [ ] Select terminal Mock transport or VNC canvas from safe backend session metadata without exposing upstream targets.
- [ ] Cover connect, disconnect, binary frames, resize, clipboard restrictions and user-visible fallback errors.

## Task 5: Dynamic connection UI

- [ ] Let administrators select Mock, AWS or VirtFusion and render provider-specific settings/credentials.
- [ ] Support multiple AWS regions, optional session token, VirtFusion base URL and token with write-only behavior.
- [ ] Add edit semantics that preserve credentials when blank, validation hints and safe provider-specific health errors.
- [ ] Add responsive tests for multiple same-type accounts and ensure secrets never render after submission.

## Task 6: Integration, documentation and verification

- [ ] Register both factories in the application and include their settings/credentials in encrypted cross-database backups.
- [ ] Document least-privilege AWS IAM actions, AWS serial-console fallback and VirtFusion API/VNC version requirements.
- [ ] Run all Go/frontend tests, lint, production build and Docker build.
- [ ] Run protocol-faithful local API fixtures for AWS and VirtFusion, including pagination and failure cases.
- [ ] With user-supplied test credentials, run opt-in live smoke tests that perform read-only discovery before any power action.
- [ ] Search logs, responses, databases and assets for submitted credentials; stop all temporary fixtures and listeners.
