package virtfusion

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"controlpanel/internal/providers"
)

func TestProviderUsesUserAPIMapsPowersAndOpensVNC(t *testing.T) {
	const firstID = "11111111-1111-4111-8111-111111111111"
	const secondID = "22222222-2222-4222-8222-222222222222"
	var mutex sync.Mutex
	requests := make([]string, 0)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		require.Equal(t, "Bearer vf-secret-token", request.Header.Get("Authorization"))
		mutex.Lock()
		requests = append(requests, request.Method+" "+request.URL.RequestURI())
		mutex.Unlock()

		switch request.Method + " " + request.URL.Path {
		case "GET /api/account":
			writeFixtureJSON(t, response, http.StatusOK, map[string]any{"data": map[string]any{"name": "Test User"}})
		case "GET /api/server":
			require.Empty(t, request.URL.RawQuery)
			writeFixtureJSON(t, response, http.StatusOK, map[string]any{"data": []any{
				map[string]any{"uuid": firstID}, map[string]any{"uuid": secondID},
			}})
		case "GET /api/server/" + firstID:
			writeFixtureJSON(t, response, http.StatusOK, serverDetail(firstID, "api-node", "running", false))
		case "GET /api/server/" + secondID:
			writeFixtureJSON(t, response, http.StatusOK, serverDetail(secondID, "worker-node", "shutoff", false))
		case "POST /api/server/" + firstID + "/power/boot", "POST /api/server/" + firstID + "/power/shutdown", "POST /api/server/" + firstID + "/power/restart":
			writeFixtureJSON(t, response, http.StatusOK, map[string]any{"data": map[string]any{"queueId": 171}})
		case "GET /api/server/" + firstID + "/vnc":
			writeFixtureJSON(t, response, http.StatusOK, map[string]any{"data": map[string]any{"vnc": map[string]any{"password": "temporary-vnc-secret", "wss": map[string]any{"url": "/vnc/?token=temporary-token"}}}})
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	factory := newFactory(factoryOptions{allowHTTP: true, allowLoopback: true})
	provider, err := factory.Create(providers.ConnectionConfig{
		ID: "vf-one", Type: "virtfusion", Endpoint: server.URL + "/api",
		Settings: json.RawMessage(`{"page_size":2}`), Credentials: json.RawMessage(`{"token":"vf-secret-token"}`),
	})
	require.NoError(t, err)

	info, err := provider.ValidateConnection(context.Background())
	require.NoError(t, err)
	require.Equal(t, "VirtFusion", info.DisplayName)

	listed, err := provider.ListServers(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, listed.Servers, 2)
	require.Equal(t, firstID, listed.Servers[0].ExternalID)
	require.Equal(t, "api-node", listed.Servers[0].Name)
	require.Equal(t, providers.StateRunning, listed.Servers[0].State)
	require.True(t, listed.Servers[0].Capabilities.CanStop.Available)
	require.True(t, listed.Servers[0].Capabilities.CanEmbedConsole.Available)
	require.True(t, listed.Servers[0].Capabilities.CanOpenConsoleWindow.Available)
	require.JSONEq(t, `{"memory_mb":1024,"storage_gb":25,"traffic_gb":1000,"cpu":2}`, string(listed.Servers[0].Spec))
	require.JSONEq(t, `[{"type":"ipv4","address":"198.51.100.41"},{"type":"ipv6","address":"2001:db8::41"}]`, string(listed.Servers[0].Addresses))
	require.Equal(t, providers.StateStopped, listed.Servers[1].State)
	require.True(t, listed.Servers[1].Capabilities.CanStart.Available)
	require.Nil(t, listed.Next)

	ref := providers.ServerRef{ExternalID: firstID}
	_, err = provider.StartServer(context.Background(), ref)
	require.NoError(t, err)
	_, err = provider.StopServer(context.Background(), ref)
	require.NoError(t, err)
	receipt, err := provider.RebootServer(context.Background(), ref)
	require.NoError(t, err)
	require.Equal(t, "171", receipt.RequestID)

	target, err := provider.OpenConsole(context.Background(), ref, providers.ConsoleEmbedded)
	require.NoError(t, err)
	require.Equal(t, "ws", target.URL.Scheme)
	require.Equal(t, "rfb", target.Protocol)
	require.Equal(t, "temporary-vnc-secret", target.Password)
	require.Equal(t, "/vnc/", target.URL.Path)
	require.Equal(t, "temporary-token", target.URL.Query().Get("token"))
	portal, err := provider.ProviderPortalURL(context.Background(), ref)
	require.NoError(t, err)
	require.Equal(t, server.URL+"/server/"+firstID, portal.String())

	mutex.Lock()
	joined := strings.Join(requests, "\n")
	mutex.Unlock()
	require.Contains(t, joined, "POST /api/server/"+firstID+"/power/shutdown")
}

func TestFactoryRejectsGlobalAdminAPIEndpoint(t *testing.T) {
	factory := newFactory(factoryOptions{allowHTTP: true, allowLoopback: true})
	_, err := factory.Create(providers.ConnectionConfig{
		ID: "vf", Type: "virtfusion", Endpoint: "http://127.0.0.1:8080/api/v1",
		Settings: json.RawMessage(`{}`), Credentials: json.RawMessage(`{"token":"test"}`),
	})
	require.EqualError(t, err, "VirtFusion requires a User API endpoint, not /api/v1")
}

func TestFactoryRejectsUnsafeEndpointsAndDoesNotEchoToken(t *testing.T) {
	factory := NewFactory(FactoryOptions{})
	_, err := factory.Create(providers.ConnectionConfig{
		ID: "vf", Type: "virtfusion", Endpoint: "http://127.0.0.1:8080",
		Settings: json.RawMessage(`{}`), Credentials: json.RawMessage(`{"token":"do-not-echo"}`),
	})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "do-not-echo")
}

func TestFactoryUsesSharedNetworkPolicyWithoutDisclosingResolvedAddresses(t *testing.T) {
	const token = "do-not-echo"
	configuration := providers.ConnectionConfig{
		ID: "vf", Endpoint: "https://private.example.test",
		Settings: json.RawMessage(`{}`), Credentials: json.RawMessage(`{"token":"` + token + `"}`),
	}
	resolver := providerStaticResolver{"private.example.test": {net.ParseIP("10.20.30.40")}}

	_, err := newFactory(factoryOptions{resolver: resolver}).Create(configuration)
	require.EqualError(t, err, "VirtFusion endpoint is not allowed")
	require.NotContains(t, err.Error(), "10.20.30.40")
	require.NotContains(t, err.Error(), token)

	_, allowedPrivateNetwork, err := net.ParseCIDR("10.20.0.0/16")
	require.NoError(t, err)
	_, err = newFactory(factoryOptions{
		resolver: resolver, allowedPrivateCIDRs: []*net.IPNet{allowedPrivateNetwork},
	}).Create(configuration)
	require.NoError(t, err)
}

func TestProviderClassifiesHTTPFailuresWithoutLeakingBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		writeFixtureJSON(t, response, http.StatusUnauthorized, map[string]any{"error": "body-secret"})
	}))
	defer server.Close()
	factory := newFactory(factoryOptions{allowHTTP: true, allowLoopback: true})
	provider, err := factory.Create(providers.ConnectionConfig{
		ID: "vf", Endpoint: server.URL, Settings: json.RawMessage(`{}`), Credentials: json.RawMessage(`{"token":"token-secret"}`),
	})
	require.NoError(t, err)
	_, err = provider.ValidateConnection(context.Background())
	require.Error(t, err)
	var providerError *providers.Error
	require.ErrorAs(t, err, &providerError)
	require.Equal(t, providers.ErrorAuthentication, providerError.Code)
	require.NotContains(t, err.Error(), "body-secret")
	require.NotContains(t, err.Error(), "token-secret")
}

func TestProviderRejectsCrossOriginVNCURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if strings.HasSuffix(request.URL.Path, "/vnc") {
			writeFixtureJSON(t, response, http.StatusOK, map[string]any{"data": map[string]any{"vnc": map[string]any{"wss": map[string]any{"url": "wss://attacker.example/vnc"}}}})
			return
		}
		http.NotFound(response, request)
	}))
	defer server.Close()
	factory := newFactory(factoryOptions{allowHTTP: true, allowLoopback: true})
	provider, err := factory.Create(providers.ConnectionConfig{ID: "vf", Endpoint: server.URL, Settings: json.RawMessage(`{}`), Credentials: json.RawMessage(`{"token":"test"}`)})
	require.NoError(t, err)
	_, err = provider.OpenConsole(context.Background(), providers.ServerRef{ExternalID: "11111111-1111-4111-8111-111111111111"}, providers.ConsoleEmbedded)
	require.Error(t, err)
}

func serverDetail(id, name, remoteState string, suspended bool) map[string]any {
	return map[string]any{"data": map[string]any{
		"uuid": id, "name": name, "state": remoteState, "commissioned": true, "suspended": suspended, "build_failed": false,
		"resources": map[string]any{"memory": 1024, "storage": 25, "traffic": 1000, "cpu_cores": 2},
		"network": map[string]any{"interfaces": []any{map[string]any{
			"ipv4": []any{map[string]any{"address": "198.51.100.41"}},
			"ipv6": []any{map[string]any{"address": "2001:db8::41"}},
		}}},
	}}
}

func writeFixtureJSON(t *testing.T, response http.ResponseWriter, status int, value any) {
	t.Helper()
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	require.NoError(t, json.NewEncoder(response).Encode(value))
}

type providerStaticResolver map[string][]net.IP

func (resolver providerStaticResolver) LookupIP(_ context.Context, _ string, host string) ([]net.IP, error) {
	return resolver[host], nil
}
