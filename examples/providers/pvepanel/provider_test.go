package pvepanel

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"

	"controlpanel/internal/providers"
	"controlpanel/internal/providers/contracttest"
	"github.com/stretchr/testify/require"
)

func TestFactoryStrictlyParsesHypotheticalConfiguration(t *testing.T) {
	client := newFixtureClient()
	factory := NewFactory(func(endpoint *url.URL, token string) (Client, error) {
		require.Equal(t, "https://panel.example.test", endpoint.String())
		require.Equal(t, "example-token", token)
		return client, nil
	})

	created, err := factory.Create(providers.ConnectionConfig{
		ID:          "pve-example-1",
		Type:        ProviderType,
		Endpoint:    "https://panel.example.test",
		Settings:    json.RawMessage(`{"tenant":"customer-a"}`),
		Credentials: json.RawMessage(`{"token":"example-token"}`),
	})
	require.NoError(t, err)
	require.NotNil(t, created)

	invalid := []providers.ConnectionConfig{
		{ID: "one", Endpoint: "http://panel.example.test", Settings: json.RawMessage(`{"tenant":"customer-a"}`), Credentials: json.RawMessage(`{"token":"example-token"}`)},
		{ID: "one", Endpoint: "https://user@panel.example.test", Settings: json.RawMessage(`{"tenant":"customer-a"}`), Credentials: json.RawMessage(`{"token":"example-token"}`)},
		{ID: "one", Endpoint: "https://panel.example.test/path", Settings: json.RawMessage(`{"tenant":"customer-a"}`), Credentials: json.RawMessage(`{"token":"example-token"}`)},
		{ID: "one", Endpoint: "https://panel.example.test", Settings: json.RawMessage(`{"tenant":"customer-a","unknown":true}`), Credentials: json.RawMessage(`{"token":"example-token"}`)},
		{ID: "one", Endpoint: "https://panel.example.test", Settings: json.RawMessage(`{"tenant":"customer-a"}`), Credentials: json.RawMessage(`{"token":""}`)},
		{ID: "one", Endpoint: "https://panel.example.test", Settings: json.RawMessage(`{"tenant":"customer-a"}`), Credentials: json.RawMessage(`{"token":"example-token","password":"must-not-be-accepted"}`)},
	}
	for index, config := range invalid {
		_, createErr := factory.Create(config)
		require.Error(t, createErr, "case %d", index)
		require.NotContains(t, createErr.Error(), "example-token")
	}
}

func TestFixturePassesProviderContract(t *testing.T) {
	client := newFixtureClient()
	factory := NewFactory(func(*url.URL, string) (Client, error) { return client, nil })
	provider, err := factory.Create(providers.ConnectionConfig{
		ID:          "pve-example-contract",
		Endpoint:    "https://panel.example.test",
		Settings:    json.RawMessage(`{"tenant":"customer-a"}`),
		Credentials: json.RawMessage(`{"token":"example-token"}`),
	})
	require.NoError(t, err)

	contracttest.Run(t, contracttest.Fixture{
		Provider: provider,
		ErrorCases: []contracttest.ErrorCase{{
			Name: "missing server",
			Call: func(ctx context.Context) error {
				_, getErr := provider.GetServer(ctx, providers.ServerRef{ExternalID: "missing", Scope: "cluster-a"})
				return getErr
			},
		}},
	})
}

func TestProviderDelegatesPowerAndConsoleMethods(t *testing.T) {
	client := newFixtureClient()
	factory := NewFactory(func(*url.URL, string) (Client, error) { return client, nil })
	provider, err := factory.Create(providers.ConnectionConfig{
		ID: "pve-example-methods", Endpoint: "https://panel.example.test",
		Settings: json.RawMessage(`{"tenant":"customer-a"}`), Credentials: json.RawMessage(`{"token":"example-token"}`),
	})
	require.NoError(t, err)
	ref := providers.ServerRef{ExternalID: "vm-1", Scope: "cluster-a"}

	start, err := provider.StartServer(context.Background(), ref)
	require.NoError(t, err)
	require.Equal(t, "start-vm-1", start.RequestID)
	stop, err := provider.StopServer(context.Background(), ref)
	require.NoError(t, err)
	require.Equal(t, "stop-vm-1", stop.RequestID)
	reboot, err := provider.RebootServer(context.Background(), ref)
	require.NoError(t, err)
	require.Equal(t, "reboot-vm-1", reboot.RequestID)

	target, err := provider.OpenConsole(context.Background(), ref, providers.ConsoleEmbedded)
	require.NoError(t, err)
	require.Equal(t, "wss://panel.example.test/console/session", target.URL.String())
	portal, err := provider.ProviderPortalURL(context.Background(), ref)
	require.NoError(t, err)
	require.Equal(t, "https://panel.example.test/servers/vm-1", portal.String())
}

type fixtureClient struct {
	servers []VendorServer
}

func newFixtureClient() *fixtureClient {
	return &fixtureClient{servers: []VendorServer{
		{ID: "vm-1", Cluster: "cluster-a", Name: "example-one", Status: "running", VCPUs: 2, MemoryMB: 2048, Addresses: []string{"192.0.2.10"}, ConsoleEnabled: true},
		{ID: "vm-2", Cluster: "cluster-b", Name: "example-two", Status: "stopped", VCPUs: 1, MemoryMB: 1024, Addresses: []string{"2001:db8::10"}},
	}}
}

func (*fixtureClient) Info(context.Context) (PanelInfo, error) {
	return PanelInfo{Name: "Hypothetical PVE Panel", Version: "example-1"}, nil
}

func (client *fixtureClient) List(_ context.Context, tenant, cursor string) (VendorPage, error) {
	if tenant != "customer-a" {
		return VendorPage{}, &providers.Error{Code: providers.ErrorPermission, Message: "example tenant rejected"}
	}
	if cursor == "" {
		return VendorPage{Servers: client.servers[:1], NextCursor: "second"}, nil
	}
	if cursor == "second" {
		return VendorPage{Servers: client.servers[1:]}, nil
	}
	return VendorPage{}, &providers.Error{Code: providers.ErrorInvalidConfig, Message: "example cursor rejected"}
}

func (client *fixtureClient) Get(_ context.Context, tenant string, ref providers.ServerRef) (VendorServer, error) {
	if tenant == "customer-a" {
		for _, server := range client.servers {
			if server.ID == ref.ExternalID && server.Cluster == ref.Scope {
				return server, nil
			}
		}
	}
	return VendorServer{}, &providers.Error{Code: providers.ErrorNotFound, Message: "example server not found"}
}

func (*fixtureClient) Power(_ context.Context, _ string, ref providers.ServerRef, action PowerAction) (string, error) {
	return string(action) + "-" + ref.ExternalID, nil
}

func (*fixtureClient) Console(context.Context, string, providers.ServerRef, providers.ConsoleMode) (ConsoleSession, error) {
	target, _ := url.Parse("wss://panel.example.test/console/session")
	return ConsoleSession{Protocol: "rfb", URL: target, Password: "short-lived-fixture-password"}, nil
}

func (*fixtureClient) Portal(_ context.Context, _ string, ref providers.ServerRef) (*url.URL, error) {
	return url.Parse("https://panel.example.test/servers/" + ref.ExternalID)
}
