# GCP and Virtualizor Provider Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add multiple isolated GCP Compute Engine and Virtualizor Enduser API connections with inventory, power controls, provider fallback, and safe embedded/pop-out Virtualizor VNC.

**Architecture:** Keep GCP SDK types behind a small injected client interface and keep Virtualizor's dynamic HTTP payloads inside its own adapter. Extract VirtFusion's outbound endpoint protections into a shared provider-network package, then extend the existing one-use console gateway with a policy-checked raw TCP transport for Virtualizor VNC. Both adapters continue to emit the existing normalized `providers.Provider` model, so jobs, operations, inventory, audit, encrypted credentials, and backups require no schema changes.

**Tech Stack:** Go 1.27.1, `google.golang.org/api/compute/v1` v0.298.0, `google.golang.org/api/option`, bounded `net/http`, raw `net.Conn`, `github.com/coder/websocket`, React 19, TypeScript 6, Vitest, Testing Library, noVNC, SQLite/MySQL.

**Spec:** `docs/superpowers/specs/2026-09-19-gcp-virtualizor-design.md`

## Global Constraints

- GCP credentials are one complete service-account JSON object per connection; do not use ADC, metadata credentials, environment credentials, shared files, user OAuth, or WIF.
- GCP settings contain one explicit project ID; inventory uses aggregated instance listing and power actions use start, stop, and hard reset.
- Virtualizor uses the customer-facing Enduser API with API key/password, never the Admin API.
- Production provider endpoints require valid HTTPS; never add insecure TLS or a global verification bypass.
- Private provider and VNC destinations require `PROVIDER_ALLOWED_PRIVATE_CIDRS`; loopback, link-local, multicast, unspecified, mixed blocked DNS answers, cross-origin redirects, and downgrade redirects fail closed.
- Credential-bearing Virtualizor URLs, service-account JSON, private keys, OAuth tokens, API passwords, VNC passwords, and upstream VNC targets must never appear in logs, audit metadata, inventory, persisted console sessions, browser URLs, or rendered connection data.
- Virtualizor VNC uses one-use local WebSocket tickets for both embedded and pop-out noVNC; the browser may receive only the local ticket, protocol, and temporary VNC password.
- Live power calls are out of scope unless the user separately supplies credentials and identifies a target VM.
- Follow red-green-refactor for every behavior change and make one focused commit after each task passes its scoped tests.

---

### Task 1: Shared outbound provider network policy

**Files:**
- Create: `internal/providers/network/policy.go`
- Create: `internal/providers/network/http.go`
- Create: `internal/providers/network/policy_test.go`
- Modify: `internal/providers/virtfusion/provider.go`
- Modify: `internal/providers/virtfusion/provider_test.go`

**Interfaces:**
- Consumes: `FactoryOptions.AllowedPrivateCIDRs` and VirtFusion's existing endpoint normalization.
- Produces: `network.NewPolicy(resolver, network.PolicyOptions)`, `Policy.Validate(context.Context, *url.URL)`, `Policy.ResolveAllowed(context.Context, string)`, `network.NewHTTPClient(Policy, *url.URL, network.HTTPOptions)`, and `network.SameOrigin(*url.URL, *url.URL)`.

- [ ] **Step 1: Write failing policy and transport tests**

Add table-driven tests proving that public and explicitly allowed private addresses pass, while loopback, link-local, unspecified, multicast, unapproved private addresses, and mixed public/private DNS answers fail. Add redirect tests for same-origin HTTPS success, cross-origin rejection, and HTTPS-to-HTTP downgrade rejection.

```go
func TestPolicyRejectsAnyBlockedResolvedAddress(t *testing.T) {
	policy := NewPolicy(staticResolver{
		"mixed.example.test": {net.ParseIP("203.0.113.10"), net.ParseIP("10.0.0.7")},
	}, PolicyOptions{})

	err := policy.Validate(context.Background(), mustURL(t, "https://mixed.example.test"))
	require.ErrorIs(t, err, ErrAddressRejected)
}

func TestHTTPClientRejectsCrossOriginAndDowngradeRedirects(t *testing.T) {
	policy := NewPolicy(staticResolver{
		"origin.example.test": {net.ParseIP("203.0.113.10")},
		"other.example.test":  {net.ParseIP("203.0.113.11")},
	}, PolicyOptions{})
	origin := mustURL(t, "https://origin.example.test")
	client := NewHTTPClient(policy, origin, HTTPOptions{})

	for _, raw := range []string{
		"https://other.example.test/next",
		"http://origin.example.test/next",
	} {
		request := &http.Request{URL: mustURL(t, raw)}
		require.Error(t, client.CheckRedirect(request, nil))
	}
}
```

- [ ] **Step 2: Run the new package test and verify the expected failure**

Run:

```bash
docker run --rm -v "$PWD":/src -w /src golang:1.27 go test ./internal/providers/network -run 'TestPolicy|TestHTTPClient' -v
```

Expected: FAIL because `internal/providers/network` and its exported policy API do not exist.

- [ ] **Step 3: Implement the shared policy and bounded HTTP client**

Use these concrete contracts:

```go
package network

var ErrAddressRejected = errors.New("provider network address rejected")

type Resolver interface {
	LookupIP(context.Context, string, string) ([]net.IP, error)
}

type PolicyOptions struct {
	AllowedPrivateCIDRs []*net.IPNet
	AllowLoopback       bool // test fixtures only; production callers leave false
}

type Policy struct {
	resolver            Resolver
	allowedPrivateCIDRs []*net.IPNet
	allowLoopback       bool
}

func NewPolicy(resolver Resolver, options PolicyOptions) Policy
func (policy Policy) ResolveAllowed(ctx context.Context, host string) ([]net.IP, error)
func (policy Policy) Validate(ctx context.Context, target *url.URL) error
func SameOrigin(left, right *url.URL) bool

type HTTPOptions struct {
	AllowHTTP bool // test fixtures only; production callers leave false
	Timeout   time.Duration
	RootCAs   *x509.CertPool // explicit test-fixture trust; production callers leave nil
}

func NewHTTPClient(policy Policy, origin *url.URL, options HTTPOptions) *http.Client
```

`NewHTTPClient` must re-resolve and validate every `DialContext`, dial an already-approved literal address to prevent a second resolver race, require the original origin on redirects, require HTTPS unless `AllowHTTP` is set by a test fixture, cap the total request at 30 seconds by default, require TLS 1.2+, and retain the existing connection-pool limits.

- [ ] **Step 4: Run the shared policy tests and verify they pass**

Run:

```bash
docker run --rm -v "$PWD":/src -w /src golang:1.27 go test ./internal/providers/network -v
```

Expected: PASS for address, redirect, and rebinding coverage.

- [ ] **Step 5: Refactor VirtFusion onto the shared package without changing behavior**

Replace the local `Resolver`, `endpointPolicy`, `newHTTPClient`, `sameHost`, and `effectivePort` implementations with `internal/providers/network`. Preserve the private test factory flags and translate them into the shared options:

```go
policy := providernetwork.NewPolicy(factory.options.resolver, providernetwork.PolicyOptions{
	AllowedPrivateCIDRs: factory.options.allowedPrivateCIDRs,
	AllowLoopback:       factory.options.allowLoopback,
})
client := providernetwork.NewHTTPClient(policy, portalURL, providernetwork.HTTPOptions{
	AllowHTTP: factory.options.allowHTTP,
})
```

Keep VirtFusion's user-facing error messages generic so shared policy errors do not disclose resolved addresses.

- [ ] **Step 6: Run shared and VirtFusion regression tests**

Run:

```bash
docker run --rm -v "$PWD":/src -w /src golang:1.27 go test ./internal/providers/network ./internal/providers/virtfusion -v
```

Expected: PASS, including existing pagination, VNC, unsafe endpoint, and token non-disclosure tests.

- [ ] **Step 7: Commit the shared network policy**

```bash
git add internal/providers/network internal/providers/virtfusion
git commit -m "refactor: share provider network policy"
```

---

### Task 2: GCP Compute Engine adapter

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`
- Create: `internal/providers/gcp/client.go`
- Create: `internal/providers/gcp/provider.go`
- Create: `internal/providers/gcp/provider_test.go`

**Interfaces:**
- Consumes: `providers.Factory`, `providers.Provider`, and the official generated Compute Engine REST client.
- Produces: `gcp.NewFactory() *Factory`; an adapter whose `ExternalID` is the instance name and whose `Scope` is the zone name.

- [ ] **Step 1: Add the pinned official GCP API dependency**

Run:

```bash
docker run --rm -v "$PWD":/src -w /src golang:1.27 go get google.golang.org/api@v0.298.0
```

Expected: `go.mod` and `go.sum` add the generated Compute Engine client and its transitive authentication/transport dependencies.

- [ ] **Step 2: Write failing GCP factory and mapping tests**

Cover strict outer credential validation, a valid full service-account object, the 6–30 character lowercase project-ID rule, aggregated pages spanning multiple zones, a benign `NO_RESULTS` scope warning, rejection of other partial-success warnings, state/spec/address mapping, and two factory-created connections using different projects/clients.

```go
func TestProviderAggregatesZonesAndMapsInstances(t *testing.T) {
	client := &fakeComputeClient{pages: []*compute.InstanceAggregatedList{{
		Items: map[string]compute.InstancesScopedList{
			"zones/us-central1-a": {Instances: []*compute.Instance{{
				Id: 42, Name: "api-1", Zone: zoneURL("us-central1-a"),
				Status: "RUNNING", MachineType: machineTypeURL("e2-small"),
			}}},
		},
		NextPageToken: "next-token",
	}}}
	provider := newProvider("project-a", client)

	page, err := provider.ListServers(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, "api-1", page.Servers[0].ExternalID)
	require.Equal(t, "us-central1-a", page.Servers[0].Scope)
	require.Equal(t, providers.StateRunning, page.Servers[0].State)
	require.Equal(t, "next-token", page.Next.Value)
}
```

- [ ] **Step 3: Run the GCP tests and verify the expected failure**

Run:

```bash
docker run --rm -v "$PWD":/src -w /src golang:1.27 go test ./internal/providers/gcp -run 'TestFactory|TestProvider' -v
```

Expected: FAIL because the GCP factory, client wrapper, and adapter do not exist.

- [ ] **Step 4: Implement the injected Compute client wrapper**

Define SDK interaction behind this exact internal interface:

```go
type computeClient interface {
	AggregatedList(context.Context, string, string) (*compute.InstanceAggregatedList, error)
	Get(context.Context, string, string, string) (*compute.Instance, error)
	Start(context.Context, string, string, string) (*compute.Operation, error)
	Stop(context.Context, string, string, string) (*compute.Operation, error)
	Reset(context.Context, string, string, string) (*compute.Operation, error)
}

type clientFactory func(context.Context, []byte) (computeClient, error)
```

The production factory must construct the service only with explicit credentials:

```go
service, err := compute.NewService(ctx,
	option.WithCredentialsJSON(serviceAccountJSON),
	option.WithScopes(compute.ComputeScope),
)
```

The wrapper calls `Instances.AggregatedList(project).ReturnPartialSuccess(true)`, applies `PageToken` only when non-empty, and uses `Instances.Get/Start/Stop/Reset(project, zone, instance)`. It must never call `compute.NewService` without `WithCredentialsJSON`.

- [ ] **Step 5: Implement settings, credentials, inventory, lookup, and capabilities**

Use these payloads and validation rules:

```go
type settings struct {
	ProjectID string `json:"project_id"`
}

type credentials struct {
	ServiceAccountJSON json.RawMessage `json:"service_account_json"`
}

var projectIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)
```

Strictly decode the outer objects. Parse the nested credential as JSON, require `type == "service_account"`, non-empty `client_email`, `private_key`, and `token_uri`, but allow other documented service-account fields. Normalize zone and machine type from the last URL path segment. Map statuses exactly as the spec states, collect `networkIP`, `natIP`, internal IPv6, and external IPv6, and emit these spec keys: `numeric_id`, `machine_type`, `labels`, `preemptible`, and `provisioning_model`.

- [ ] **Step 6: Write failing GCP action, portal, and error tests**

```go
func TestProviderUsesHardResetAndReturnsOperationName(t *testing.T) {
	client := &fakeComputeClient{resetOperation: &compute.Operation{Name: "operation-77"}}
	provider := newProvider("project-a", client)

	receipt, err := provider.RebootServer(context.Background(), providers.ServerRef{
		ExternalID: "api-1", Scope: "us-central1-a",
	})
	require.NoError(t, err)
	require.Equal(t, "operation-77", receipt.RequestID)
	require.Equal(t, "reset", client.lastAction)
}

func TestProviderPortalContainsOnlyProjectZoneAndInstance(t *testing.T) {
	portal, err := provider.ProviderPortalURL(context.Background(), ref)
	require.NoError(t, err)
	require.Equal(t, "console.cloud.google.com", portal.Hostname())
	require.Equal(t, "project-a", portal.Query().Get("project"))
}
```

Also test `googleapi.Error` 401, 403, 404, 409/412, 429, 5xx, context cancellation/deadline, and generic errors. Assert returned messages omit private-key fragments and raw response bodies.

- [ ] **Step 7: Implement actions, provider fallback, and normalized errors**

Start/stop/reset validate both instance name and zone, return the operation name, and classify errors into the existing provider codes. `OpenConsole` returns `ErrorUnsupported`. Generate the resource page as:

```go
&url.URL{
	Scheme: "https",
	Host:   "console.cloud.google.com",
	Path:   path.Join("/compute/instancesDetail/zones", ref.Scope, "instances", ref.ExternalID),
	RawQuery: url.Values{"project": {provider.projectID}}.Encode(),
}
```

Set both console capabilities unavailable with a reason directing the user to the provider portal; always set `HasProviderPortal.Available` true.

- [ ] **Step 8: Run and commit the complete GCP adapter**

Run:

```bash
docker run --rm -v "$PWD":/src -w /src golang:1.27 go test ./internal/providers/gcp -v
```

Expected: PASS for credentials, isolation, pagination, state mapping, actions, portal, and errors.

```bash
git add go.mod go.sum internal/providers/gcp
git commit -m "feat: add GCP Compute provider"
```

---

### Task 3: Virtualizor Enduser API adapter

**Files:**
- Create: `internal/providers/virtualizor/provider.go`
- Create: `internal/providers/virtualizor/provider_test.go`

**Interfaces:**
- Consumes: the shared `internal/providers/network` policy and the normalized Provider contract.
- Produces: `virtualizor.NewFactory(virtualizor.FactoryOptions) *Factory`, Enduser inventory/actions, credential-free portal URLs, and internal `vnc+tcp://host:port` console targets.

- [ ] **Step 1: Write failing protocol-faithful inventory tests**

Use `httptest.NewTLSServer` through an unexported test factory with `allowLoopback` and an explicit root CA pool containing only the fixture certificate. Do not use `InsecureSkipVerify`. Require these exact query fields on every request: `api=json`, `apikey`, and `apipass`; require `act=listvs` for validation/listing and `act=vpsmanage&svs=<id>` for lookup. Fixture responses must cover an empty object, numeric top-level VPS keys plus metadata, string/number/bool variants, malformed numeric entries, and two isolated connections with different credentials.

```go
func TestProviderListsMixedTypeVPSRecords(t *testing.T) {
	fixture := map[string]any{
		"uid": "9",
		"3332": map[string]any{
			"vpsid": "3332", "hostname": "edge-1", "virt": "kvm",
			"status": 1, "suspended": "0", "vnc": "1",
			"cores": "4", "ram": 2048, "space": "40", "bandwidth": "1000",
			"server_name": "node-a", "ips": map[string]any{"s1": "198.51.100.20"},
		},
	}
	provider := newVirtualizorFixture(t, fixture)

	page, err := provider.ListServers(context.Background(), nil)
	require.NoError(t, err)
	require.Nil(t, page.Next)
	require.Len(t, page.Servers, 1)
	server := page.Servers[0]
	require.Equal(t, "3332", server.ExternalID)
	require.Equal(t, "node-a", server.Scope)
	require.Equal(t, providers.StateRunning, server.State)
	require.True(t, server.Capabilities.CanEmbedConsole.Available)
	require.True(t, server.Capabilities.CanOpenConsoleWindow.Available)
	require.JSONEq(t, `{"virt":"kvm","cpu":4,"memory_mb":2048,"storage_gb":40,"bandwidth_gb":1000}`, string(server.Spec))
	require.JSONEq(t, `[{"type":"ipv4","address":"198.51.100.20"}]`, string(server.Addresses))
}
```

Implement the test helper with signature `func newVirtualizorFixture(t *testing.T, listResponse any) providers.Provider`; it must start a loopback TLS fixture, add `server.Certificate()` to a new `x509.CertPool`, verify all four authentication/query fields, construct through the unexported test factory, and register cleanup with `t.Cleanup`.

- [ ] **Step 2: Run inventory tests and verify the expected failure**

Run:

```bash
docker run --rm -v "$PWD":/src -w /src golang:1.27 go test ./internal/providers/virtualizor -run 'TestFactory|TestProviderLists|TestProviderGets' -v
```

Expected: FAIL because the Virtualizor package does not exist.

- [ ] **Step 3: Implement endpoint, flexible scalar parsing, and bounded requests**

The production factory accepts only an absolute HTTPS origin with no userinfo/query/fragment. Normalize its API URL to `index.php`, use the shared policy/client, and store credentials only in the provider instance:

```go
type FactoryOptions struct {
	AllowedPrivateCIDRs []*net.IPNet
}

type credentials struct {
	APIKey      string `json:"api_key"`
	APIPassword string `json:"api_password"`
}

type Provider struct {
	panelURL   *url.URL
	apiURL     *url.URL
	apiKey     string
	apiPassword string
	client     *http.Client
}
```

Implement `flexString`, `flexInt64`, and `flexBool` unmarshalling so documented string/number/bool variants normalize consistently. Bound successful JSON bodies to 4 MiB, reject trailing JSON, discard bounded error bodies, and replace every `http.Client.Do` error with a credential-free normalized message instead of returning `url.Error` text.

- [ ] **Step 4: Implement inventory and lookup normalization**

For `listvs`, decode `map[string]json.RawMessage`; sort numeric keys by VPS ID for deterministic inventory, ignore nonnumeric metadata keys, and fail the whole page if any numeric entry is malformed or has a mismatched `vpsid`. Return one page with `Next == nil`.

For `vpsmanage`, read `info.vps`, `info.ip`, `info.server_name`, and `info.flags.enable_console`. Normalize:

```go
func mapState(status int64, suspended bool) providers.ServerState {
	if suspended || status == 2 { return providers.StateSuspended }
	switch status {
	case 1: return providers.StateRunning
	case 0: return providers.StateStopped
	default: return providers.StateUnknown
	}
}
```

Set `ExternalID` to the decimal VPS ID and `Scope` to `server_name`, falling back to `serid`. Emit `virt`, `cpu`, `memory_mb`, `storage_gb`, and `bandwidth_gb` in `Spec`. VNC capabilities are available only when `vnc` is true, the VPS is not suspended, and `enable_console` is not explicitly disabled; portal capability is always available.

- [ ] **Step 5: Write failing power, VNC, redirect, and error tests**

Require these exact action queries:

```text
act=start&svs=3332&do=1
act=stop&svs=3332&do=1
act=restart&svs=3332&do=1
act=vnc&svs=3332&novnc=3332&do=add
```

The action fixtures should prove that a response containing a non-empty `done.msg` succeeds even when `status` is `0`, while an HTTP-200 payload containing `error` or missing `done` fails. The VNC fixture should return `{ "ip":"198.51.100.30", "port":"5951", "password":"temporary-vnc", "novnc":1 }` and assert an internal `vnc+tcp` target without credentials in its URL. Also cover invalid ports, unavailable VNC, cross-origin redirects, downgrade redirects, private addresses, 401/403/404/429/5xx, timeout, oversized JSON, and secret non-disclosure.

- [ ] **Step 6: Implement power actions, VNC, portal, and API-level errors**

Use a single request builder that starts from a copy of `apiURL`, sets action parameters, then appends credentials immediately before request creation. Never retain the credential-bearing URL outside the request. Success rules are:

```go
type actionResponse struct {
	Done struct { Message string `json:"msg"` } `json:"done"`
	Error json.RawMessage `json:"error"`
}

if len(response.Error) != 0 && string(response.Error) != "null" {
	return providerFailure("Virtualizor rejected the operation")
}
if strings.TrimSpace(response.Done.Message) == "" {
	return providerFailure("Virtualizor did not confirm the operation")
}
```

Return an empty action request ID because the Enduser response does not provide a stable queue ID. Build VNC targets with `url.URL{Scheme: "vnc+tcp", Host: net.JoinHostPort(ip, port)}` and return protocol `rfb`; never place the password in the URL. Build provider fallback from a clean panel URL plus `act=vpsmanage&svs=<id>`, without `api`, `apikey`, or `apipass`.

- [ ] **Step 7: Run and commit the complete Virtualizor adapter**

Run:

```bash
docker run --rm -v "$PWD":/src -w /src golang:1.27 go test ./internal/providers/network ./internal/providers/virtualizor -v
```

Expected: PASS for empty/populated accounts, mixed scalar types, isolation, actions, VNC, policy, failures, and secret scanning assertions.

```bash
git add internal/providers/virtualizor
git commit -m "feat: add Virtualizor provider"
```

---

### Task 4: Policy-checked raw TCP VNC gateway

**Files:**
- Modify: `internal/console/policy.go`
- Modify: `internal/console/policy_test.go`
- Modify: `internal/console/websocket.go`
- Modify: `internal/console/websocket_test.go`
- Modify: `internal/app/app.go`

**Interfaces:**
- Consumes: internal `vnc+tcp://host:port` targets stored in `MemoryTargetStore` and the existing console CIDR policy.
- Produces: `TargetPolicy.DialTCP(context.Context, *url.URL) (net.Conn, error)` and `GatewayOptions.TargetPolicy *TargetPolicy`.

- [ ] **Step 1: Write failing raw target policy tests**

Add an injectable dialer to policy options and prove raw targets require `vnc+tcp`, a host, a numeric port from 1–65535, and no userinfo/path/query/fragment. Prove that all DNS answers are checked immediately before the dial and that the dialer receives an approved literal IP rather than the original hostname.

```go
type TCPDialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

type TargetPolicyOptions struct {
	AllowedPrivateCIDRs []*net.IPNet
	AllowMockTransport  bool
	Dialer              TCPDialer
}

func (policy *TargetPolicy) DialTCP(ctx context.Context, target *url.URL) (net.Conn, error)
```

- [ ] **Step 2: Run the policy test and verify it fails**

Run:

```bash
docker run --rm -v "$PWD":/src -w /src golang:1.27 go test ./internal/console -run 'TestTargetPolicy.*TCP' -v
```

Expected: FAIL because raw VNC targets and policy-controlled dialing are unsupported.

- [ ] **Step 3: Implement raw-target validation and dialing**

Extend `ValidateEmbedded` to accept `vnc+tcp` only through a dedicated raw-target validator; keep `wss` and Mock behavior unchanged. `DialTCP` must re-resolve, reject if any answer is blocked, then attempt approved literal addresses sequentially with the original port. Use a 10-second dial timeout and never expose the target in returned errors.

- [ ] **Step 4: Write failing bidirectional raw VNC gateway tests**

Create a fake `net.Conn`/dialer and an authenticated one-use WebSocket fixture. Verify TCP bytes arrive as WebSocket binary messages, WebSocket binary messages arrive unchanged at TCP, text messages fail the session, a second ticket use returns 401, the in-memory target is removed, and idle/absolute cancellation closes the TCP connection.

```go
func TestWebSocketGatewayProxiesRawVNCBinaryFrames(t *testing.T) {
	proxySide, vncSide := net.Pipe()
	t.Cleanup(func() { _ = vncSide.Close() })
	fixture := newRawGatewayFixture(t, proxySide)
	connection := fixture.Dial(t)
	t.Cleanup(func() { _ = connection.CloseNow() })

	want := []byte{0, 1, 2, 255}
	go func() { _, _ = vncSide.Write(want) }()
	messageType, got, err := connection.Read(context.Background())
	require.NoError(t, err)
	require.Equal(t, websocket.MessageBinary, messageType)
	require.Equal(t, want, got)

	require.NoError(t, connection.Write(context.Background(), websocket.MessageBinary, want))
	buffer := make([]byte, len(want))
	_, err = io.ReadFull(vncSide, buffer)
	require.NoError(t, err)
	require.Equal(t, want, buffer)
}

func TestWebSocketGatewayRejectsTextForRawVNC(t *testing.T) {
	proxySide, vncSide := net.Pipe()
	fixture := newRawGatewayFixture(t, proxySide)
	connection := fixture.Dial(t)
	require.NoError(t, connection.Write(context.Background(), websocket.MessageText, []byte("not-rfb")))
	require.Eventually(t, func() bool {
		return fixture.Repository.closedResult == ResultFailed
	}, time.Second, 10*time.Millisecond)
	_, err := vncSide.Write([]byte{1})
	require.Error(t, err)
}
```

Implement `newRawGatewayFixture(t *testing.T, upstream net.Conn) rawGatewayFixture`, returning a fixture with `Dial(t) *websocket.Conn` and its `*gatewayRepository`. Its resolver maps `vnc.example.test` to a public documentation address, its injected dialer returns `upstream`, and its target store contains `vnc+tcp://vnc.example.test:5951` for the fixture's one-use session.

- [ ] **Step 5: Run gateway tests and verify they fail**

Run:

```bash
docker run --rm -v "$PWD":/src -w /src golang:1.27 go test ./internal/console -run 'TestWebSocketGateway.*RawVNC' -v
```

Expected: FAIL because the gateway tries to treat `vnc+tcp` as a WebSocket URL.

- [ ] **Step 6: Implement the raw TCP proxy**

Pass the same `consolePolicy` to the gateway in `app.compose`:

```go
type GatewayOptions struct {
	Now             func() time.Time
	IdleTimeout     time.Duration
	AbsoluteTimeout time.Duration
	NewAuditID      func() string
	TargetPolicy    *TargetPolicy
}
```

In `proxy`, route `vnc+tcp` to `runRawVNC`. That method calls `TargetPolicy.DialTCP`, copies TCP reads to `websocket.MessageBinary`, accepts only downstream `websocket.MessageBinary`, applies the existing 1 MiB WebSocket limit and idle deadlines, cancels both directions after the first terminal result, closes the TCP connection, and returns `ResultCompleted` only for a clean WebSocket close.

- [ ] **Step 7: Run console and application tests, then commit**

Run:

```bash
docker run --rm -v "$PWD":/src -w /src golang:1.27 go test ./internal/console ./internal/app -v
```

Expected: PASS for Mock terminal, VirtFusion WSS, Virtualizor raw VNC, one-use ticket, policy, audit, and application composition.

```bash
git add internal/console internal/app/app.go
git commit -m "feat: proxy raw VNC over console tickets"
```

---

### Task 5: Register providers and expose safe provider metadata

**Files:**
- Modify: `internal/app/app.go`
- Modify: `internal/app/app_test.go`
- Modify: `internal/connections/http.go`
- Modify: `internal/connections/http_test.go`

**Interfaces:**
- Consumes: `gcp.NewFactory()` and `virtualizor.NewFactory(FactoryOptions)`.
- Produces: registered provider types `gcp` and `virtualizor`, with labels `Google Cloud Compute Engine` and `Virtualizor`.

- [ ] **Step 1: Write failing metadata and composition tests**

Extend provider-type assertions to require the complete sorted response:

```json
{
  "provider_types": [
    {"id":"aws","name":"AWS EC2"},
    {"id":"gcp","name":"Google Cloud Compute Engine"},
    {"id":"mock","name":"Mock Provider"},
    {"id":"virtualizor","name":"Virtualizor"},
    {"id":"virtfusion","name":"VirtFusion"}
  ]
}
```

Add an application composition assertion that `compose` succeeds with no host/cloud credentials and that the provider-type endpoint exposes both new providers only after authentication.

- [ ] **Step 2: Run the scoped tests and verify they fail**

Run:

```bash
docker run --rm -v "$PWD":/src -w /src golang:1.27 go test ./internal/connections ./internal/app -run 'TestHTTPConnectionActionsAndProviderTypes|TestCompose' -v
```

Expected: FAIL because neither new factory is registered or named.

- [ ] **Step 3: Register both factories and labels**

In `app.compose`, register GCP directly and pass the parsed provider CIDRs to Virtualizor:

```go
registry.Register("gcp", providergcp.NewFactory())
registry.Register("virtualizor", providervirtualizor.NewFactory(
	providervirtualizor.FactoryOptions{AllowedPrivateCIDRs: allowedProviderCIDRs},
))
```

Keep factory construction offline: GCP must not contact Google until validation/list/action, while Virtualizor performs only local endpoint/DNS policy validation during construction. Add explicit names in `connections.HTTPHandler.providerTypes`; do not return credential schemas or stored values.

- [ ] **Step 4: Run all backend tests and commit**

Run:

```bash
docker run --rm -v "$PWD":/src -w /src golang:1.27 go test ./... -count=1
```

Expected: all Go packages PASS.

```bash
git add internal/app internal/connections
git commit -m "feat: register GCP and Virtualizor providers"
```

---

### Task 6: Provider-specific forms, hard-reset warning, and Virtualizor pop-out VNC

**Files:**
- Modify: `web/src/connections/ConnectionsPage.tsx`
- Modify: `web/src/connections/ConnectionsPage.test.tsx`
- Modify: `web/src/servers/ServersPage.tsx`
- Modify: `web/src/servers/ServersPage.test.tsx`
- Modify: `web/src/console/api.test.ts`

**Interfaces:**
- Consumes: the existing `createConnection` payload and one-use embedded-console API.
- Produces: GCP `{settings:{project_id}, credentials:{service_account_json}}`, Virtualizor `{endpoint, credentials:{api_key,api_password}}`, GCP-specific reboot copy, and pop-out noVNC for both VirtFusion and Virtualizor.

- [ ] **Step 1: Write failing connection-form tests**

Add one GCP and one Virtualizor test. For GCP, paste a syntactically valid service-account object into the textarea and require the submitted object—not a JSON string or path:

```ts
expect(submitted).toMatchObject({
  name: '生产 GCP',
  provider_type: 'gcp',
  endpoint: '',
  settings: { project_id: 'example-project' },
  credentials: { service_account_json: {
    type: 'service_account',
    client_email: 'panel@example-project.iam.gserviceaccount.com',
    private_key: 'write-only-private-key',
    token_uri: 'https://oauth2.googleapis.com/token',
  } },
})
expect(screen.queryByText('write-only-private-key')).not.toBeInTheDocument()
```

For Virtualizor, require `https://panel.example.test:4083`, API key, and password, and assert both secrets disappear after submission. Add a malformed GCP JSON test that stays in the form and shows `Service Account JSON 格式无效` without issuing POST.

- [ ] **Step 2: Run form tests and verify they fail**

Run:

```bash
npm --prefix web test -- --run src/connections/ConnectionsPage.test.tsx
```

Expected: FAIL because the new provider-specific controls are absent.

- [ ] **Step 3: Implement GCP and Virtualizor form branches**

Add controlled fields for `gcpProjectID`, `gcpServiceAccountJSON`, `virtualizorEndpoint`, `virtualizorAPIKey`, and `virtualizorAPIPassword`. Parse the GCP textarea inside `submit`, require a non-array object, and send the exact payload tested above. Render credential controls with `autoComplete="new-password"`; after a successful submit, close/unmount the form so secrets leave React state. Virtualizor displays the existing HTTPS/private-CIDR note and sends empty settings `{}`.

- [ ] **Step 4: Write failing server interaction tests**

Add a GCP server/connection fixture, click reboot, and require this warning inside the confirmation dialog:

```text
Google Compute Engine 将执行硬重置，效果类似立即重启电源，不会等待操作系统正常关机。
```

Add a Virtualizor server fixture whose window-console capability is available, click `新窗口控制台`, and assert the frontend opens `/console-popout`, waits for the same-origin ready message, creates `/console-sessions`, and posts only `{serverName, session}` to the popup. Assert no upstream IP, port, API key, or API password appears in the posted payload.

Add a second Virtualizor fixture with both VNC capabilities unavailable. Assert both console buttons are disabled with their provider reason while `服务商后台` remains enabled, proving the UI fallback remains reachable.

- [ ] **Step 5: Run server tests and verify they fail**

Run:

```bash
npm --prefix web test -- --run src/servers/ServersPage.test.tsx src/console/api.test.ts
```

Expected: FAIL because GCP has no hard-reset warning and Virtualizor is not routed to the local pop-out console.

- [ ] **Step 6: Implement the warning and pop-out routing**

Pass `connection?.provider_type` into `ConfirmationDialog` and show the warning only when `action === 'reboot' && providerType === 'gcp'`. Replace the hard-coded VirtFusion branch with a helper:

```ts
function usesLocalVNCWindow(providerType: string | undefined) {
  return providerType === 'virtfusion' || providerType === 'virtualizor'
}
```

Keep AWS/GCP external console-window behavior unchanged and retain provider-portal fallback. Do not add upstream target fields to `ConsoleSession`.

- [ ] **Step 7: Run frontend tests, lint, and build; commit the UI**

Run:

```bash
npm --prefix web test -- --run
npm --prefix web run lint
npm --prefix web run build
```

Expected: all Vitest files PASS, oxlint reports no errors, and TypeScript/Vite production build succeeds.

```bash
git add web/src/connections web/src/servers web/src/console
git commit -m "feat: add GCP and Virtualizor controls"
```

---

### Task 7: Documentation, built assets, and end-to-end verification

**Files:**
- Modify: `README.md`
- Modify: `docs/providers.md`
- Modify: `docs/console.md`
- Modify: `internal/webassets/dist/**` (generated by `scripts/build.sh`)

**Interfaces:**
- Consumes: the completed backend/frontend feature and existing build pipeline.
- Produces: operator guidance, synchronized embedded frontend assets, and evidence that no secrets/listeners/artifacts remain.

- [ ] **Step 1: Update provider and console documentation**

Document these exact GCP IAM permissions:

```text
compute.instances.list
compute.instances.get
compute.instances.start
compute.instances.stop
compute.instances.reset
```

Explain that service-account keys are long-lived credentials that should be dedicated, minimally scoped, rotated, and protected; that reboot means Compute Engine hard reset; and that console access falls back to the official Google Cloud resource page.

For Virtualizor, document Enduser API key/password creation, HTTPS-only endpoints, `PROVIDER_ALLOWED_PRIVATE_CIDRS`, `listvs`/`vpsmanage`/start/stop/restart/VNC prerequisites, raw VNC proxying through one-use local tickets, and credential-free provider fallback. State that Virtualizor requires credentials in its upstream query string but this application redacts them everywhere else.

Update README's provider list and stage-three summary. Update `docs/console.md` so embedded targets allow validated internal `vnc+tcp` only for backend raw VNC proxying; browsers still connect solely to same-origin WebSocket tickets.

- [ ] **Step 2: Run focused secret scans before building**

Run:

```bash
grep -RInE 'write-only-private-key|write-only-api-password|temporary-vnc-secret' internal web/dist docs --exclude='*_test.go' --exclude='*.test.ts' --exclude='*.test.tsx'
```

Expected: no output. Test-only fixture strings may exist only in test source files.

- [ ] **Step 3: Run full backend verification**

Run:

```bash
docker run --rm -v "$PWD":/src -w /src golang:1.27 go test ./... -count=1
```

Expected: all Go packages PASS without real cloud credentials.

- [ ] **Step 4: Run full frontend verification**

Run:

```bash
npm --prefix web test -- --run
npm --prefix web run lint
npm --prefix web run build
```

Expected: all frontend tests PASS, lint is clean, and the production bundle succeeds.

- [ ] **Step 5: Synchronize embedded assets and verify the production build**

Run:

```bash
./scripts/build.sh
docker build -t server-control-panel:gcp-virtualizor .
```

Expected: generated `web/dist` is copied into `internal/webassets/dist`, all tests rerun successfully, the static Go binary builds, and the Docker image builds.

- [ ] **Step 6: Inspect assets, repository state, and listeners**

Run:

```bash
grep -RInE 'private_key|api_password|apikey=|apipass=|temporary-vnc' internal/webassets/dist web/dist
git status --short
ss -ltnp
docker ps --filter ancestor=server-control-panel:gcp-virtualizor
```

Expected: secret scan has no output; Git shows only intended documentation/generated asset changes; no project listener is bound to 8080; no container from the verification image is running.

- [ ] **Step 7: Remove only the temporary verification image and commit**

After confirming the exact image name from the preceding read-only checks, run:

```bash
docker image rm server-control-panel:gcp-virtualizor
git add README.md docs/providers.md docs/console.md internal/webassets/dist
git commit -m "docs: document GCP and Virtualizor providers"
```

Expected: the temporary image is removed, generated assets are committed, and `git status --short` is empty.
