package mock

import (
	"context"
	"encoding/json"
	"testing"

	"controlpanel/internal/providers"
	"github.com/stretchr/testify/require"
)

func TestProviderReturnsStableNormalizedInventory(t *testing.T) {
	factory := NewFactory()
	provider, err := factory.Create(providers.ConnectionConfig{
		ID: "lab-a", Type: "mock", Settings: json.RawMessage(`{"server_count":4,"seed":8,"console_profile":"embedded"}`),
		Credentials: json.RawMessage(`{"token":"valid"}`),
	})
	require.NoError(t, err)

	info, err := provider.ValidateConnection(context.Background())
	require.NoError(t, err)
	require.Equal(t, "Mock Lab lab-a", info.DisplayName)

	first, err := provider.ListServers(context.Background(), nil)
	require.NoError(t, err)
	second, err := provider.ListServers(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.Len(t, first.Servers, 4)
	require.Equal(t, "lab-a-server-001", first.Servers[0].ExternalID)
	require.Contains(t, []providers.ServerState{providers.StateRunning, providers.StateStopped, providers.StatePending, providers.StateError}, first.Servers[0].State)
	require.True(t, first.Servers[0].Capabilities.CanEmbedConsole.Available)
	require.True(t, first.Servers[0].Capabilities.CanOpenConsoleWindow.Available)
}

func TestProviderConsoleProfiles(t *testing.T) {
	tests := []struct {
		profile string
		embed   bool
		window  bool
		portal  bool
	}{
		{profile: "embedded", embed: true, window: true, portal: true},
		{profile: "window", window: true, portal: true},
		{profile: "portal", portal: true},
		{profile: "none"},
	}
	for _, test := range tests {
		t.Run(test.profile, func(t *testing.T) {
			provider, err := NewFactory().Create(providers.ConnectionConfig{
				ID: "lab", Type: "mock", Settings: json.RawMessage(`{"server_count":1,"console_profile":"` + test.profile + `"}`),
				Credentials: json.RawMessage(`{"token":"valid"}`),
			})
			require.NoError(t, err)
			page, err := provider.ListServers(context.Background(), nil)
			require.NoError(t, err)
			capabilities := page.Servers[0].Capabilities
			require.Equal(t, test.embed, capabilities.CanEmbedConsole.Available)
			require.Equal(t, test.window, capabilities.CanOpenConsoleWindow.Available)
			require.Equal(t, test.portal, capabilities.HasProviderPortal.Available)
		})
	}
}

func TestProviderHealthModesReturnClassifiedErrors(t *testing.T) {
	for _, test := range []struct {
		mode string
		code providers.ErrorCode
	}{
		{mode: "authentication_failure", code: providers.ErrorAuthentication},
		{mode: "network_failure", code: providers.ErrorNetwork},
		{mode: "rate_limited", code: providers.ErrorRateLimited},
	} {
		t.Run(test.mode, func(t *testing.T) {
			provider, err := NewFactory().Create(providers.ConnectionConfig{
				ID: "lab", Type: "mock", Settings: json.RawMessage(`{"server_count":1,"health_mode":"` + test.mode + `"}`),
				Credentials: json.RawMessage(`{"token":"valid"}`),
			})
			require.NoError(t, err)
			_, err = provider.ValidateConnection(context.Background())
			var providerError *providers.Error
			require.ErrorAs(t, err, &providerError)
			require.Equal(t, test.code, providerError.Code)
		})
	}
}

func TestProviderRejectsUnknownSettingsAndMissingToken(t *testing.T) {
	_, err := NewFactory().Create(providers.ConnectionConfig{
		ID: "lab", Type: "mock", Settings: json.RawMessage(`{"unexpected":true}`),
		Credentials: json.RawMessage(`{"token":"valid"}`),
	})
	require.Error(t, err)

	_, err = NewFactory().Create(providers.ConnectionConfig{
		ID: "lab", Type: "mock", Settings: json.RawMessage(`{"server_count":1}`),
		Credentials: json.RawMessage(`{}`),
	})
	require.Error(t, err)
}

func TestFactoryPreservesPowerStateAcrossProviderInstances(t *testing.T) {
	factory := NewFactory()
	configuration := providers.ConnectionConfig{
		ID: "lab-persistent", Type: "mock", Settings: json.RawMessage(`{"server_count":1,"seed":1}`),
		Credentials: json.RawMessage(`{"token":"valid"}`),
	}
	first, err := factory.Create(configuration)
	require.NoError(t, err)
	page, err := first.ListServers(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, providers.StateStopped, page.Servers[0].State)
	ref := providers.ServerRef{ExternalID: page.Servers[0].ExternalID, Scope: page.Servers[0].Scope}
	_, err = first.StartServer(context.Background(), ref)
	require.NoError(t, err)

	second, err := factory.Create(configuration)
	require.NoError(t, err)
	server, err := second.GetServer(context.Background(), ref)
	require.NoError(t, err)
	require.Equal(t, providers.StateRunning, server.State)

	isolated, err := factory.Create(providers.ConnectionConfig{
		ID: "lab-isolated", Type: "mock", Settings: configuration.Settings, Credentials: configuration.Credentials,
	})
	require.NoError(t, err)
	isolatedPage, err := isolated.ListServers(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, providers.StateStopped, isolatedPage.Servers[0].State)
}
