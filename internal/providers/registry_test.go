package providers_test

import (
	"context"
	"encoding/json"
	"testing"

	"controlpanel/internal/providers"
	"controlpanel/internal/providers/mock"
	"github.com/stretchr/testify/require"
)

func TestRegistryCreatesIndependentMockConnections(t *testing.T) {
	registry := providers.NewRegistry()
	require.NoError(t, registry.Register("mock", mock.NewFactory()))
	first, err := registry.Create(providers.ConnectionConfig{
		ID: "connection-one", Type: "mock", Settings: json.RawMessage(`{"server_count":2,"seed":1}`),
		Credentials: json.RawMessage(`{"token":"first"}`),
	})
	require.NoError(t, err)
	second, err := registry.Create(providers.ConnectionConfig{
		ID: "connection-two", Type: "mock", Settings: json.RawMessage(`{"server_count":3,"seed":2}`),
		Credentials: json.RawMessage(`{"token":"second"}`),
	})
	require.NoError(t, err)

	firstPage, err := first.ListServers(context.Background(), nil)
	require.NoError(t, err)
	secondPage, err := second.ListServers(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, firstPage.Servers, 2)
	require.Len(t, secondPage.Servers, 3)
	require.NotEqual(t, firstPage.Servers[0].ExternalID, secondPage.Servers[0].ExternalID)
}

func TestRegistryRejectsDuplicateAndUnknownProviders(t *testing.T) {
	registry := providers.NewRegistry()
	require.NoError(t, registry.Register("mock", mock.NewFactory()))
	require.ErrorIs(t, registry.Register("mock", mock.NewFactory()), providers.ErrProviderAlreadyRegistered)
	_, err := registry.Create(providers.ConnectionConfig{ID: "one", Type: "missing"})
	require.ErrorIs(t, err, providers.ErrUnknownProvider)
}
