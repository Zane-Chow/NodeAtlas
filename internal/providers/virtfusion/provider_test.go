package virtfusion

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"controlpanel/internal/providers"
)

func TestProviderPaginatesMapsPowersAndOpensVNC(t *testing.T) {
	var mutex sync.Mutex
	requests := make([]string, 0)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		require.Equal(t, "Bearer vf-secret-token", request.Header.Get("Authorization"))
		mutex.Lock()
		requests = append(requests, request.Method+" "+request.URL.RequestURI())
		mutex.Unlock()

		switch request.Method + " " + request.URL.Path {
		case "GET /api/v1/connect":
			writeFixtureJSON(t, response, http.StatusOK, []any{})
		case "GET /api/v1/servers":
			page, _ := strconv.Atoi(request.URL.Query().Get("page"))
			require.Equal(t, "simple", request.URL.Query().Get("type"))
			require.Equal(t, "2", request.URL.Query().Get("results"))
			if page == 1 {
				writeFixtureJSON(t, response, http.StatusOK, map[string]any{"current_page": 1, "last_page": 2, "data": []any{map[string]any{"id": 41}}})
				return
			}
			writeFixtureJSON(t, response, http.StatusOK, map[string]any{"current_page": 2, "last_page": 2, "data": []any{map[string]any{"id": 42}}})
		case "GET /api/v1/servers/41":
			require.Equal(t, "true", request.URL.Query().Get("remoteState"))
			writeFixtureJSON(t, response, http.StatusOK, serverDetail(41, "api-node", "running", false))
		case "GET /api/v1/servers/42":
			writeFixtureJSON(t, response, http.StatusOK, serverDetail(42, "worker-node", "shutoff", false))
		case "POST /api/v1/servers/41/power/boot", "POST /api/v1/servers/41/power/shutdown", "POST /api/v1/servers/41/power/restart":
			writeFixtureJSON(t, response, http.StatusOK, map[string]any{"data": map[string]any{"queueId": 171}})
		case "GET /api/v1/servers/41/vnc":
			writeFixtureJSON(t, response, http.StatusOK, map[string]any{"data": map[string]any{"vnc": map[string]any{"password": "temporary-vnc-secret", "wss": map[string]any{"url": "/vnc/?token=temporary-token"}}}})
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	factory := newFactory(factoryOptions{allowHTTP: true, allowLoopback: true})
	provider, err := factory.Create(providers.ConnectionConfig{
		ID: "vf-one", Type: "virtfusion", Endpoint: server.URL,
		Settings: json.RawMessage(`{"page_size":2}`), Credentials: json.RawMessage(`{"token":"vf-secret-token"}`),
	})
	require.NoError(t, err)

	info, err := provider.ValidateConnection(context.Background())
	require.NoError(t, err)
	require.Equal(t, "VirtFusion", info.DisplayName)

	first, err := provider.ListServers(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, first.Servers, 1)
	require.Equal(t, "41", first.Servers[0].ExternalID)
	require.Equal(t, "api-node", first.Servers[0].Name)
	require.Equal(t, providers.StateRunning, first.Servers[0].State)
	require.True(t, first.Servers[0].Capabilities.CanStop.Available)
	require.True(t, first.Servers[0].Capabilities.CanEmbedConsole.Available)
	require.True(t, first.Servers[0].Capabilities.CanOpenConsoleWindow.Available)
	require.JSONEq(t, `{"memory_mb":1024,"storage_gb":25,"traffic_gb":1000,"cpu":2}`, string(first.Servers[0].Spec))
	require.JSONEq(t, `[{"type":"ipv4","address":"198.51.100.41"},{"type":"ipv6","address":"2001:db8::41"}]`, string(first.Servers[0].Addresses))
	require.NotNil(t, first.Next)

	second, err := provider.ListServers(context.Background(), first.Next)
	require.NoError(t, err)
	require.Equal(t, providers.StateStopped, second.Servers[0].State)
	require.True(t, second.Servers[0].Capabilities.CanStart.Available)
	require.Nil(t, second.Next)

	ref := providers.ServerRef{ExternalID: "41"}
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
	require.Equal(t, server.URL, portal.String())

	mutex.Lock()
	joined := strings.Join(requests, "\n")
	mutex.Unlock()
	require.Contains(t, joined, "POST /api/v1/servers/41/power/shutdown")
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
	_, err = provider.OpenConsole(context.Background(), providers.ServerRef{ExternalID: "1"}, providers.ConsoleEmbedded)
	require.Error(t, err)
}

func serverDetail(id int, name, remoteState string, suspended bool) map[string]any {
	return map[string]any{"data": map[string]any{
		"id": id, "name": name, "state": "complete", "commissionStatus": 3, "suspended": suspended, "buildFailed": false, "remoteState": remoteState,
		"settings": map[string]any{"resources": map[string]any{"memory": 1024, "storage": 25, "traffic": 1000, "cpuCores": 2}},
		"vnc":      map[string]any{"enabled": true},
		"network": map[string]any{"interfaces": []any{map[string]any{
			"ipv4": []any{map[string]any{"address": "198.51.100." + strconv.Itoa(id)}},
			"ipv6": []any{map[string]any{"address": "2001:db8::" + strconv.Itoa(id)}},
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
