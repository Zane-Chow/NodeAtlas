package gcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	"controlpanel/internal/providers"
)

const serviceAccount = `{"type":"service_account","project_id":"credential-project","private_key_id":"key-id","private_key":"private-key-do-not-echo","client_email":"test@credential-project.iam.gserviceaccount.com","client_id":"123","auth_uri":"https://accounts.google.com/o/oauth2/auth","token_uri":"https://oauth2.googleapis.com/token","auth_provider_x509_cert_url":"https://www.googleapis.com/oauth2/v1/certs","client_x509_cert_url":"https://www.googleapis.com/robot/v1/metadata/x509/test","universe_domain":"googleapis.com"}`

func validConfig() providers.ConnectionConfig {
	return providers.ConnectionConfig{ID: "gcp-one", Type: "gcp", Settings: json.RawMessage(`{"project_id":"project-a"}`), Credentials: json.RawMessage(`{"service_account_json":` + serviceAccount + `}`)}
}

func TestFactoryValidatesConfiguration(t *testing.T) {
	for _, test := range []struct{ name, settings, credentials, id string }{
		{name: "valid full service account", id: "one"},
		{name: "missing id"},
		{name: "unknown settings", id: "one", settings: `{"project_id":"project-a","zone":"secret"}`},
		{name: "settings trailing JSON", id: "one", settings: `{"project_id":"project-a"} {}`},
		{name: "null settings", id: "one", settings: `null`},
		{name: "unknown outer credential", id: "one", credentials: `{"service_account_json":` + serviceAccount + `,"extra":"private-key-do-not-echo"}`},
		{name: "trailing credentials", id: "one", credentials: `{"service_account_json":` + serviceAccount + `} {}`},
		{name: "null credentials", id: "one", credentials: `null`},
		{name: "missing service account", id: "one", credentials: `{}`},
		{name: "null service account", id: "one", credentials: `{"service_account_json":null}`},
		{name: "path credentials", id: "one", credentials: `{"service_account_json":"/tmp/key.json"}`},
		{name: "user credentials", id: "one", credentials: `{"service_account_json":{"type":"authorized_user"}}`},
		{name: "missing email", id: "one", credentials: strings.Replace(`{"service_account_json":`+serviceAccount+`}`, `"client_email":"test@credential-project.iam.gserviceaccount.com",`, "", 1)},
		{name: "blank key", id: "one", credentials: strings.Replace(`{"service_account_json":`+serviceAccount+`}`, "private-key-do-not-echo", "  ", 1)},
		{name: "missing token URI", id: "one", credentials: strings.Replace(`{"service_account_json":`+serviceAccount+`}`, `"token_uri":"https://oauth2.googleapis.com/token",`, "", 1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := validConfig()
			config.ID = test.id
			if test.settings != "" {
				config.Settings = json.RawMessage(test.settings)
			}
			if test.credentials != "" {
				config.Credentials = json.RawMessage(test.credentials)
			}
			calls := 0
			factory := newFactory(func(_ context.Context, raw []byte) (computeClient, error) {
				calls++
				require.JSONEq(t, serviceAccount, string(raw))
				return &fakeComputeClient{}, nil
			})
			provider, err := factory.Create(config)
			if test.name == "valid full service account" {
				require.NoError(t, err)
				require.NotNil(t, provider)
				require.Equal(t, 1, calls)
			} else {
				require.Error(t, err)
				require.Nil(t, provider)
				require.Zero(t, calls)
				require.NotContains(t, err.Error(), "private-key-do-not-echo")
			}
		})
	}
}

func TestFactoryProjectIDRules(t *testing.T) {
	for _, project := range []string{"abcde", "abcdef", "a" + strings.Repeat("b", 28) + "1", "a" + strings.Repeat("b", 29) + "1", "1abcde", "Abcdef", "abcde-", "abc_de", " project-a", "project-a", "a----1"} {
		t.Run(project, func(t *testing.T) {
			config := validConfig()
			config.Settings, _ = json.Marshal(map[string]string{"project_id": project})
			factory := newFactory(func(context.Context, []byte) (computeClient, error) { return &fakeComputeClient{}, nil })
			_, err := factory.Create(config)
			valid := project == "abcdef" || project == "a"+strings.Repeat("b", 28)+"1" || project == "project-a" || project == "a----1"
			if valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestFactoryKeepsConnectionsIsolated(t *testing.T) {
	clients := []*fakeComputeClient{}
	var secrets []string
	factory := newFactory(func(_ context.Context, raw []byte) (computeClient, error) {
		secrets = append(secrets, string(raw))
		client := &fakeComputeClient{pages: []*compute.InstanceAggregatedList{{Items: map[string]compute.InstancesScopedList{"zones/us-central1-a": {Instances: []*compute.Instance{testInstance("api-"+string(rune('a'+len(clients))), "us-central1-a")}}}}}}
		clients = append(clients, client)
		return client, nil
	})
	first, err := factory.Create(validConfig())
	require.NoError(t, err)
	config := validConfig()
	config.ID = "second"
	config.Settings = json.RawMessage(`{"project_id":"project-b"}`)
	config.Credentials = json.RawMessage(strings.Replace(string(config.Credentials), "private-key-do-not-echo", "second-private-key", 1))
	second, err := factory.Create(config)
	require.NoError(t, err)
	a, err := first.ListServers(context.Background(), nil)
	require.NoError(t, err)
	b, err := second.ListServers(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, "api-a", a.Servers[0].ExternalID)
	require.Equal(t, "api-b", b.Servers[0].ExternalID)
	require.Equal(t, "project-a", clients[0].project)
	require.Equal(t, "project-b", clients[1].project)
	require.NotEqual(t, secrets[0], secrets[1])
	require.NotSame(t, first, second)
}

func TestProviderAggregatesZonesAndMapsInstances(t *testing.T) {
	instance := testInstance("api-1", "us-central1-a")
	instance.Labels = map[string]string{"role": "api"}
	instance.Scheduling = &compute.Scheduling{Preemptible: true, ProvisioningModel: "SPOT"}
	instance.NetworkInterfaces = []*compute.NetworkInterface{{NetworkIP: "10.0.0.2", Ipv6Address: "fd00::2", AccessConfigs: []*compute.AccessConfig{{NatIP: "203.0.113.2"}}, Ipv6AccessConfigs: []*compute.AccessConfig{{ExternalIpv6: "2001:db8::2"}}}}
	client := &fakeComputeClient{pages: []*compute.InstanceAggregatedList{
		{Items: map[string]compute.InstancesScopedList{
			"zones/us-central1-a":  {Instances: []*compute.Instance{instance}},
			"zones/europe-west1-b": {Instances: []*compute.Instance{testInstance("worker-1", "europe-west1-b")}},
			"zones/empty":          {Warning: &compute.InstancesScopedListWarning{Code: "NO_RESULTS"}},
		}, NextPageToken: "opaque+/=token"},
		{Items: map[string]compute.InstancesScopedList{"zones/asia-east1-a": {Instances: []*compute.Instance{testInstance("worker-2", "asia-east1-a")}}}},
	}}
	provider := newProvider("project-a", client)
	page, err := provider.ListServers(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, page.Servers, 2)
	require.Equal(t, "opaque+/=token", page.Next.Value)
	var server providers.RemoteServer
	for _, value := range page.Servers {
		if value.ExternalID == "api-1" {
			server = value
		}
	}
	require.Equal(t, "us-central1-a", server.Scope)
	require.Equal(t, providers.StateRunning, server.State)
	require.Equal(t, "api-1", server.Name)
	require.Equal(t, "RUNNING", server.RemoteState)
	require.JSONEq(t, `{"numeric_id":42,"machine_type":"e2-small","labels":{"role":"api"},"preemptible":true,"provisioning_model":"SPOT"}`, string(server.Spec))
	require.JSONEq(t, `[{"type":"private","address":"10.0.0.2"},{"type":"private","address":"fd00::2"},{"type":"public","address":"203.0.113.2"},{"type":"public","address":"2001:db8::2"}]`, string(server.Addresses))
	require.True(t, server.Capabilities.CanStop.Available)
	require.True(t, server.Capabilities.CanReboot.Available)
	require.False(t, server.Capabilities.CanStart.Available)
	require.False(t, server.Capabilities.CanEmbedConsole.Available)
	require.Contains(t, server.Capabilities.CanEmbedConsole.Reason, "provider portal")
	require.False(t, server.Capabilities.CanOpenConsoleWindow.Available)
	require.Contains(t, server.Capabilities.CanOpenConsoleWindow.Reason, "provider portal")
	require.True(t, server.Capabilities.HasProviderPortal.Available)
	page, err = provider.ListServers(context.Background(), page.Next)
	require.NoError(t, err)
	require.Len(t, page.Servers, 1)
	require.Equal(t, "asia-east1-a", page.Servers[0].Scope)
	require.Nil(t, page.Next)
	require.Equal(t, []string{"", "opaque+/=token"}, client.tokens)
}

func TestProviderRejectsIncompleteInventory(t *testing.T) {
	for _, code := range []string{"UNREACHABLE", "PARTIAL_SUCCESS", "UNKNOWN"} {
		t.Run(code, func(t *testing.T) {
			client := &fakeComputeClient{pages: []*compute.InstanceAggregatedList{{Items: map[string]compute.InstancesScopedList{"zones/us-central1-a": {Warning: &compute.InstancesScopedListWarning{Code: code, Message: "private-key-do-not-echo"}}}}}}
			provider := newProvider("project-a", client)
			page, err := provider.ListServers(context.Background(), nil)
			require.Error(t, err)
			require.Empty(t, page.Servers)
			require.NotContains(t, err.Error(), "private-key-do-not-echo")
		})
	}
}

func TestProviderRejectsUnreachableOrMalformedInventory(t *testing.T) {
	for name, page := range map[string]*compute.InstanceAggregatedList{
		"unreachable":      {Unreachables: []string{"us-central1-a"}},
		"global warning":   {Warning: &compute.InstanceAggregatedListWarning{Code: "PARTIAL_SUCCESS", Message: "private-key-do-not-echo"}},
		"nil instance":     {Items: map[string]compute.InstancesScopedList{"zones/us-central1-a": {Instances: []*compute.Instance{nil}}}},
		"missing identity": {Items: map[string]compute.InstancesScopedList{"zones/us-central1-a": {Instances: []*compute.Instance{{Status: "RUNNING"}}}}},
	} {
		t.Run(name, func(t *testing.T) {
			provider := newProvider("project-a", &fakeComputeClient{pages: []*compute.InstanceAggregatedList{page}})
			_, err := provider.ListServers(context.Background(), nil)
			require.Error(t, err)
			_, err = provider.ValidateConnection(context.Background())
			require.Error(t, err)
			require.NotContains(t, err.Error(), "private-key-do-not-echo")
		})
	}
}

func TestProviderStateMappingAndLookup(t *testing.T) {
	for raw, state := range map[string]providers.ServerState{"PROVISIONING": providers.StatePending, "STAGING": providers.StatePending, "RUNNING": providers.StateRunning, "STOPPING": providers.StateStopping, "SUSPENDING": providers.StateStopping, "SUSPENDED": providers.StateSuspended, "TERMINATED": providers.StateStopped, "REPAIRING": providers.StateUnknown, "NEW_STATE": providers.StateUnknown} {
		t.Run(raw, func(t *testing.T) {
			instance := testInstance("api-1", "us-central1-a")
			instance.Status = raw
			client := &fakeComputeClient{instance: instance}
			server, err := newProvider("project-a", client).GetServer(context.Background(), providers.ServerRef{ExternalID: "api-1", Scope: "us-central1-a"})
			require.NoError(t, err)
			require.Equal(t, state, server.State)
			require.Equal(t, raw, server.RemoteState)
			require.Equal(t, state == providers.StateStopped, server.Capabilities.CanStart.Available)
			require.Equal(t, "project-a", client.project)
			require.Equal(t, "us-central1-a", client.zone)
			require.Equal(t, "api-1", client.name)
		})
	}
}

func TestProviderValidatesEmptyProjectInventory(t *testing.T) {
	provider := newProvider("project-a", &fakeComputeClient{})
	info, err := provider.ValidateConnection(context.Background())
	require.NoError(t, err)
	require.Contains(t, info.DisplayName, "project-a")
	require.NotEmpty(t, info.Version)
	page, err := provider.ListServers(context.Background(), nil)
	require.NoError(t, err)
	require.Empty(t, page.Servers)
	require.Nil(t, page.Next)
}

func TestActionsReturnOperationNamesAndUseHardReset(t *testing.T) {
	client := &fakeComputeClient{operation: &compute.Operation{Name: "operation-77"}}
	provider := newProvider("project-a", client)
	ref := providers.ServerRef{ExternalID: "api-1", Scope: "us-central1-a"}
	for action, call := range map[string]func(context.Context, providers.ServerRef) (providers.ActionReceipt, error){"start": provider.StartServer, "stop": provider.StopServer, "reset": provider.RebootServer} {
		t.Run(action, func(t *testing.T) {
			receipt, err := call(context.Background(), ref)
			require.NoError(t, err)
			require.Equal(t, "operation-77", receipt.RequestID)
			require.Equal(t, action, client.lastAction)
			require.Equal(t, "project-a", client.project)
			require.Equal(t, ref.Scope, client.zone)
			require.Equal(t, ref.ExternalID, client.name)
		})
	}
}

func TestActionsValidateRefsBeforeCallingClient(t *testing.T) {
	for _, ref := range []providers.ServerRef{{Scope: "us-central1-a"}, {ExternalID: "api-1"}, {ExternalID: "../api-1", Scope: "us-central1-a"}, {ExternalID: "api-1", Scope: "../us-central1-a"}, {ExternalID: "api-1?secret=key", Scope: "us-central1-a"}, {ExternalID: "api-1", Scope: "us-central1-a/instances/x"}} {
		client := &fakeComputeClient{}
		provider := newProvider("project-a", client)
		_, err := provider.StartServer(context.Background(), ref)
		require.Error(t, err)
		_, err = provider.StopServer(context.Background(), ref)
		require.Error(t, err)
		_, err = provider.RebootServer(context.Background(), ref)
		require.Error(t, err)
		_, err = provider.GetServer(context.Background(), ref)
		require.Error(t, err)
		_, err = provider.ProviderPortalURL(context.Background(), ref)
		require.Error(t, err)
		require.Empty(t, client.lastAction)
		require.Empty(t, client.project)
		var normalized *providers.Error
		require.ErrorAs(t, err, &normalized)
		require.Equal(t, providers.ErrorInvalidConfig, normalized.Code)
	}
}

func TestActionsRejectMissingOperation(t *testing.T) {
	for _, operation := range []*compute.Operation{nil, {}} {
		provider := newProvider("project-a", &fakeComputeClient{operation: operation})
		_, err := provider.StartServer(context.Background(), providers.ServerRef{ExternalID: "api-1", Scope: "us-central1-a"})
		require.Error(t, err)
	}
}

func TestPortalContainsOnlyProjectZoneAndInstance(t *testing.T) {
	provider := newProvider("project-a", &fakeComputeClient{})
	ref := providers.ServerRef{ExternalID: "api-1", Scope: "us-central1-a"}
	portal, err := provider.ProviderPortalURL(context.Background(), ref)
	require.NoError(t, err)
	require.Equal(t, "https://console.cloud.google.com/compute/instancesDetail/zones/us-central1-a/instances/api-1?project=project-a", portal.String())
	require.Nil(t, portal.User)
	require.Empty(t, portal.Fragment)
	require.Len(t, portal.Query(), 1)
	for _, mode := range []providers.ConsoleMode{providers.ConsoleEmbedded, providers.ConsoleWindow} {
		target, err := provider.OpenConsole(context.Background(), ref, mode)
		var normalized *providers.Error
		require.ErrorAs(t, err, &normalized)
		require.Equal(t, providers.ErrorUnsupported, normalized.Code)
		require.Contains(t, normalized.Message, "provider portal")
		require.Nil(t, target.URL)
	}
}

func TestErrorsAreNormalizedWithoutLeakingSecrets(t *testing.T) {
	for _, test := range []struct {
		name  string
		err   error
		code  providers.ErrorCode
		retry bool
	}{
		{"401", &googleapi.Error{Code: 401, Message: "private-key-do-not-echo", Body: "raw-response-body"}, providers.ErrorAuthentication, false},
		{"403", &googleapi.Error{Code: 403, Message: "private-key-do-not-echo", Body: "raw-response-body"}, providers.ErrorPermission, false},
		{"404", &googleapi.Error{Code: 404, Body: "raw-response-body"}, providers.ErrorNotFound, false},
		{"409", &googleapi.Error{Code: 409, Body: "raw-response-body"}, providers.ErrorProvider, false},
		{"412", &googleapi.Error{Code: 412, Body: "raw-response-body"}, providers.ErrorProvider, false},
		{"429", &googleapi.Error{Code: 429, Body: "raw-response-body"}, providers.ErrorRateLimited, true},
		{"500", &googleapi.Error{Code: 500, Body: "raw-response-body"}, providers.ErrorProvider, true},
		{"503", &googleapi.Error{Code: 503, Body: "raw-response-body"}, providers.ErrorProvider, true},
		{"canceled", context.Canceled, providers.ErrorNetwork, true},
		{"deadline", fmt.Errorf("private-key-do-not-echo: %w", context.DeadlineExceeded), providers.ErrorNetwork, true},
		{"network", &netError{}, providers.ErrorNetwork, true},
		{"generic", errors.New("private-key-do-not-echo raw-response-body"), providers.ErrorProvider, false},
		{"wrapped", fmt.Errorf("private-key-do-not-echo: %w", &googleapi.Error{Code: 401}), providers.ErrorAuthentication, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := newProvider("project-a", &fakeComputeClient{err: test.err})
			ref := providers.ServerRef{ExternalID: "api-1", Scope: "us-central1-a"}
			_, listErr := provider.ListServers(context.Background(), nil)
			_, validateErr := provider.ValidateConnection(context.Background())
			_, getErr := provider.GetServer(context.Background(), ref)
			_, startErr := provider.StartServer(context.Background(), ref)
			_, stopErr := provider.StopServer(context.Background(), ref)
			_, resetErr := provider.RebootServer(context.Background(), ref)
			for _, err := range []error{listErr, validateErr, getErr, startErr, stopErr, resetErr} {
				var normalized *providers.Error
				require.ErrorAs(t, err, &normalized)
				require.Equal(t, test.code, normalized.Code)
				require.Equal(t, test.retry, normalized.Retryable)
				require.NotContains(t, err.Error(), "private-key-do-not-echo")
				require.NotContains(t, err.Error(), "raw-response-body")
			}
		})
	}
}

type netError struct{}

func (*netError) Error() string   { return "private-key-do-not-echo raw-response-body" }
func (*netError) Timeout() bool   { return false }
func (*netError) Temporary() bool { return true }

func TestFactorySanitizesClientInitializationErrors(t *testing.T) {
	factory := newFactory(func(context.Context, []byte) (computeClient, error) {
		return nil, errors.New("private-key-do-not-echo raw-response-body")
	})
	_, err := factory.Create(validConfig())
	require.Error(t, err)
	require.NotContains(t, err.Error(), "private-key-do-not-echo")
	require.NotContains(t, err.Error(), "raw-response-body")
}

func TestSDKRejectsMissingExplicitCredentials(t *testing.T) {
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "/must-not-read-ambient-credentials.json")
	for _, raw := range [][]byte{nil, []byte(`{}`), []byte(`{"type":"authorized_user"}`), []byte(`null`)} {
		client, err := defaultClient(context.Background(), raw)
		require.Error(t, err)
		require.Nil(t, client)
		require.NotContains(t, err.Error(), "must-not-read-ambient")
	}
}

func TestSDKForwardsComputeRequests(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/aggregated/") {
			require.Equal(t, "true", r.URL.Query().Get("returnPartialSuccess"))
			if len(requests) == 1 {
				require.False(t, r.URL.Query().Has("pageToken"))
			} else {
				require.Equal(t, "opaque+/=token", r.URL.Query().Get("pageToken"))
			}
			fmt.Fprint(w, `{"items":{}}`)
		} else {
			fmt.Fprint(w, `{"name":"operation-or-instance"}`)
		}
	}))
	defer server.Close()
	service, err := compute.NewService(context.Background(), option.WithHTTPClient(server.Client()), option.WithEndpoint(server.URL+"/"))
	require.NoError(t, err)
	client := &sdkClient{service: service}
	ctx := context.Background()
	_, err = client.AggregatedList(ctx, "project-a", "")
	require.NoError(t, err)
	_, err = client.AggregatedList(ctx, "project-a", "opaque+/=token")
	require.NoError(t, err)
	_, err = client.Get(ctx, "project-a", "us-central1-a", "api-1")
	require.NoError(t, err)
	_, err = client.Start(ctx, "project-a", "us-central1-a", "api-1")
	require.NoError(t, err)
	_, err = client.Stop(ctx, "project-a", "us-central1-a", "api-1")
	require.NoError(t, err)
	_, err = client.Reset(ctx, "project-a", "us-central1-a", "api-1")
	require.NoError(t, err)
	require.Equal(t, []string{"GET /projects/project-a/aggregated/instances", "GET /projects/project-a/aggregated/instances", "GET /projects/project-a/zones/us-central1-a/instances/api-1", "POST /projects/project-a/zones/us-central1-a/instances/api-1/start", "POST /projects/project-a/zones/us-central1-a/instances/api-1/stop", "POST /projects/project-a/zones/us-central1-a/instances/api-1/reset"}, requests)
}

type fakeComputeClient struct {
	pages                           []*compute.InstanceAggregatedList
	instance                        *compute.Instance
	operation                       *compute.Operation
	err                             error
	project, zone, name, lastAction string
	tokens                          []string
}

func (client *fakeComputeClient) AggregatedList(_ context.Context, project, token string) (*compute.InstanceAggregatedList, error) {
	client.project = project
	client.tokens = append(client.tokens, token)
	if client.err != nil {
		return nil, client.err
	}
	if len(client.pages) == 0 {
		return &compute.InstanceAggregatedList{}, nil
	}
	index := len(client.tokens) - 1
	if index >= len(client.pages) {
		index = len(client.pages) - 1
	}
	return client.pages[index], nil
}
func (client *fakeComputeClient) Get(_ context.Context, project, zone, name string) (*compute.Instance, error) {
	client.project = project
	client.zone = zone
	client.name = name
	return client.instance, client.err
}
func (client *fakeComputeClient) action(action, project, zone, name string) (*compute.Operation, error) {
	client.lastAction = action
	client.project = project
	client.zone = zone
	client.name = name
	return client.operation, client.err
}
func (client *fakeComputeClient) Start(_ context.Context, project, zone, name string) (*compute.Operation, error) {
	return client.action("start", project, zone, name)
}
func (client *fakeComputeClient) Stop(_ context.Context, project, zone, name string) (*compute.Operation, error) {
	return client.action("stop", project, zone, name)
}
func (client *fakeComputeClient) Reset(_ context.Context, project, zone, name string) (*compute.Operation, error) {
	return client.action("reset", project, zone, name)
}
func testInstance(name, zone string) *compute.Instance {
	return &compute.Instance{Id: 42, Name: name, Zone: "https://www.googleapis.com/compute/v1/projects/project-a/zones/" + zone, Status: "RUNNING", MachineType: "https://www.googleapis.com/compute/v1/projects/project-a/zones/" + zone + "/machineTypes/e2-small"}
}
