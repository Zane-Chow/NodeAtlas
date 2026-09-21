package virtualizor

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"controlpanel/internal/providers"
	"github.com/stretchr/testify/require"
)

const fixtureKey = "key-secret-314"
const fixturePassword = "password-secret-271"

func TestProviderPowerActions(t *testing.T) {
	for _, action := range []string{"start", "stop", "restart"} {
		t.Run(action, func(t *testing.T) {
			var calls atomic.Int32
			p := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				q := r.URL.Query()
				if q.Get("act") != action || q.Get("svs") != "3332" || q.Get("do") != "1" || len(q) != 6 {
					t.Error("unexpected power query")
				}
				writeJSON(t, w, map[string]any{"status": 0, "done": map[string]any{"msg": "operation accepted"}})
			})
			operation := map[string]func(context.Context, providers.ServerRef) (providers.ActionReceipt, error){"start": p.StartServer, "stop": p.StopServer, "restart": p.RebootServer}[action]
			receipt, err := operation(context.Background(), providers.ServerRef{ExternalID: "3332"})
			require.NoError(t, err)
			require.Empty(t, receipt.RequestID)
			require.EqualValues(t, 1, calls.Load())
		})
	}
}

func TestProviderPowerRejectsUnconfirmedOrAPIError(t *testing.T) {
	for _, raw := range []string{`{"status":1}`, `{"done":{}}`, `{"done":{"msg":"  "}}`, `{"error":["body-secret"],"done":{"msg":"success"}}`, `{"error":false,"done":{"msg":"success"}}`} {
		t.Run(raw, func(t *testing.T) {
			p := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) { writeJSON(t, w, json.RawMessage(raw)) })
			_, err := p.StartServer(context.Background(), providers.ServerRef{ExternalID: "3332"})
			assertSafeError(t, err, providers.ErrorProvider)
		})
	}
}

func consoleFixture(t *testing.T, vnc any, available bool) *Provider {
	t.Helper()
	return fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("svs") != "3332" {
			t.Error("unexpected VNC server ID")
		}
		switch q.Get("act") {
		case "vpsmanage":
			writeJSON(t, w, map[string]any{"info": map[string]any{"status": 1, "vps": map[string]any{"vpsid": 3332, "vnc": available}}})
		case "vnc":
			if !available {
				t.Error("requested unavailable VNC")
			}
			if q.Get("novnc") != "3332" || q.Get("do") != "add" || len(q) != 7 {
				t.Error("unexpected VNC query")
			}
			writeJSON(t, w, vnc)
		default:
			t.Error("unexpected console action")
		}
	})
}

func TestProviderOpensVNCAndCleanPortal(t *testing.T) {
	p := consoleFixture(t, map[string]any{"ip": "198.51.100.30", "port": "5951", "password": "temporary-vnc", "novnc": 1}, true)
	for _, mode := range []providers.ConsoleMode{providers.ConsoleEmbedded, providers.ConsoleWindow} {
		target, err := p.OpenConsole(context.Background(), providers.ServerRef{ExternalID: "3332"}, mode)
		require.NoError(t, err)
		require.Equal(t, "vnc+tcp://198.51.100.30:5951", target.URL.String())
		require.Equal(t, "rfb", target.Protocol)
		require.Equal(t, mode, target.Mode)
		require.Equal(t, "temporary-vnc", target.Password)
		encoded, err := json.Marshal(target)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), "temporary-vnc")
		require.NotContains(t, target.URL.String(), fixtureKey)
		require.NotContains(t, target.URL.String(), fixturePassword)
	}
	portal, err := p.ProviderPortalURL(context.Background(), providers.ServerRef{ExternalID: "3332"})
	require.NoError(t, err)
	require.Equal(t, url.Values{"act": {"vpsmanage"}, "svs": {"3332"}}, portal.Query())
	require.Equal(t, "/index.php", portal.Path)
	require.Nil(t, portal.User)
	require.NotContains(t, portal.String(), fixtureKey)
	require.NotContains(t, portal.String(), fixturePassword)
	portal.RawQuery = "modified"
	require.Empty(t, p.panelURL.RawQuery)
	require.Empty(t, p.apiURL.RawQuery)
}

func TestProviderOpensKVMVNCWithOpenVZConsoleDisabled(t *testing.T) {
	var vncCalls atomic.Int32
	p := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("act") {
		case "vpsmanage":
			writeJSON(t, w, map[string]any{"info": map[string]any{
				"status": 1,
				"vps":    map[string]any{"vpsid": 3332, "virt": "kvm", "vnc": "1", "suspended": "0"},
				"flags":  map[string]any{"enable_console": 0},
			}})
		case "vnc":
			vncCalls.Add(1)
			writeJSON(t, w, map[string]any{"ip": "198.51.100.30", "port": 5951, "password": "temporary-vnc", "novnc": 1})
		default:
			t.Error("unexpected console action")
		}
	})
	for _, mode := range []providers.ConsoleMode{providers.ConsoleEmbedded, providers.ConsoleWindow} {
		target, err := p.OpenConsole(context.Background(), providers.ServerRef{ExternalID: "3332"}, mode)
		require.NoError(t, err)
		require.Equal(t, "vnc+tcp://198.51.100.30:5951", target.URL.String())
		require.Equal(t, mode, target.Mode)
	}
	require.EqualValues(t, 2, vncCalls.Load())
}

func TestProviderRejectsInvalidVNC(t *testing.T) {
	for _, port := range []any{"0", "65536", "-1", "bad", 1.5, nil} {
		t.Run("port", func(t *testing.T) {
			p := consoleFixture(t, map[string]any{"ip": "198.51.100.30", "port": port, "novnc": true}, true)
			_, err := p.OpenConsole(context.Background(), providers.ServerRef{ExternalID: "3332"}, providers.ConsoleEmbedded)
			assertSafeError(t, err, providers.ErrorProvider)
		})
	}
	for _, ip := range []string{"bad-host", "10.0.0.1", "169.254.169.254", "0.0.0.0"} {
		p := consoleFixture(t, map[string]any{"ip": ip, "port": 5951, "novnc": true}, true)
		_, err := p.OpenConsole(context.Background(), providers.ServerRef{ExternalID: "3332"}, providers.ConsoleEmbedded)
		assertSafeError(t, err, providers.ErrorProvider)
	}
	p := consoleFixture(t, map[string]any{"ip": "198.51.100.30", "port": 5951, "novnc": false}, true)
	_, err := p.OpenConsole(context.Background(), providers.ServerRef{ExternalID: "3332"}, providers.ConsoleEmbedded)
	assertSafeError(t, err, providers.ErrorUnsupported)
	p = consoleFixture(t, nil, false)
	_, err = p.OpenConsole(context.Background(), providers.ServerRef{ExternalID: "3332"}, providers.ConsoleEmbedded)
	assertSafeError(t, err, providers.ErrorUnsupported)
	_, err = p.OpenConsole(context.Background(), providers.ServerRef{ExternalID: "3332"}, providers.ConsoleMode("bad"))
	assertSafeError(t, err, providers.ErrorUnsupported)
}

func assertSafeError(t *testing.T, err error, code providers.ErrorCode) {
	t.Helper()
	require.Error(t, err)
	var failure *providers.Error
	require.ErrorAs(t, err, &failure)
	require.Equal(t, code, failure.Code)
	for _, secret := range []string{fixtureKey, fixturePassword, "body-secret", "apikey=", "apipass="} {
		require.NotContains(t, err.Error(), secret)
		require.NotContains(t, failure.SafeMessage(), secret)
	}
	var urlError *url.Error
	require.False(t, errors.As(err, &urlError))
}

func TestProviderClassifiesHTTPFailures(t *testing.T) {
	for _, tc := range []struct {
		status int
		code   providers.ErrorCode
		retry  bool
	}{
		{401, providers.ErrorAuthentication, false}, {403, providers.ErrorPermission, false}, {404, providers.ErrorNotFound, false}, {429, providers.ErrorRateLimited, true}, {500, providers.ErrorProvider, true}, {503, providers.ErrorProvider, true},
	} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			p := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte("body-secret " + fixtureKey + " " + fixturePassword))
			})
			_, err := p.ValidateConnection(context.Background())
			assertSafeError(t, err, tc.code)
			require.Equal(t, tc.retry, err.(*providers.Error).Retryable)
		})
	}
}

func TestProviderRejectsRedirectsWithoutLeakingSecrets(t *testing.T) {
	var destinationCalls atomic.Int32
	destination := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { destinationCalls.Add(1) }))
	t.Cleanup(destination.Close)
	for _, target := range []string{destination.URL, "http://127.0.0.1:80/", "https://10.0.0.1/"} {
		p := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target+"?"+r.URL.RawQuery, http.StatusFound)
		})
		_, err := p.ValidateConnection(context.Background())
		assertSafeError(t, err, providers.ErrorNetwork)
	}
	require.Zero(t, destinationCalls.Load())
}

func TestProviderTimeoutAndTLSFailuresAreSafe(t *testing.T) {
	p := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	p.client.Timeout = 30 * time.Millisecond
	_, err := p.ValidateConnection(context.Background())
	assertSafeError(t, err, providers.ErrorNetwork)
	require.Contains(t, err.Error(), "timed out")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("untrusted TLS request reached handler") }))
	t.Cleanup(server.Close)
	untrusted, err := newFactory(factoryOptions{allowLoopback: true, rootCAs: x509.NewCertPool()}).Create(fixtureConfig(server.URL))
	require.NoError(t, err)
	t.Cleanup(untrusted.(*Provider).client.CloseIdleConnections)
	_, err = untrusted.ValidateConnection(context.Background())
	assertSafeError(t, err, providers.ErrorNetwork)
}

func TestProviderRejectsMalformedOversizedAndAPIErrorResponses(t *testing.T) {
	for _, body := range []string{"{} {}", "{", "null", `{"error":"body-secret"}`, `{"metadata":"` + strings.Repeat("x", maxResponseBytes) + `"}`, "{}" + strings.Repeat(" ", maxResponseBytes)} {
		p := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) })
		_, err := p.ListServers(context.Background(), nil)
		assertSafeError(t, err, providers.ErrorProvider)
	}
}

func fixtureConfig(endpoint string) providers.ConnectionConfig {
	return providers.ConnectionConfig{ID: "virtualizor-one", Endpoint: endpoint, Credentials: json.RawMessage(`{"api_key":"` + fixtureKey + `","api_password":"` + fixturePassword + `"}`)}
}

func fixtureProvider(t *testing.T, handler http.HandlerFunc) *Provider {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.Method != http.MethodGet || r.URL.Path != "/index.php" || q.Get("api") != "json" || q.Get("apikey") != fixtureKey || q.Get("apipass") != fixturePassword || q.Get("act") == "" {
			t.Error("unexpected Enduser request method, path, or authentication fields")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	p, err := newFactory(factoryOptions{allowLoopback: true, rootCAs: roots}).Create(fixtureConfig(server.URL))
	require.NoError(t, err)
	t.Cleanup(p.(*Provider).client.CloseIdleConnections)
	return p.(*Provider)
}

func newVirtualizorFixture(t *testing.T, listResponse any) providers.Provider {
	t.Helper()
	return fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("act") != "listvs" || len(r.URL.Query()) != 4 {
			t.Error("expected exact listvs query")
		}
		writeJSON(t, w, listResponse)
	})
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Error(err)
	}
}

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

func TestProviderListsEmptyAndValidates(t *testing.T) {
	p := newVirtualizorFixture(t, map[string]any{})
	page, err := p.ListServers(context.Background(), nil)
	require.NoError(t, err)
	require.Empty(t, page.Servers)
	require.Nil(t, page.Next)
	info, err := p.ValidateConnection(context.Background())
	require.NoError(t, err)
	require.Equal(t, "Virtualizor", info.DisplayName)
}

func TestProviderListsSortedStatesAndFlexibleScalars(t *testing.T) {
	p := newVirtualizorFixture(t, map[string]any{
		"10":       map[string]any{"vpsid": 10, "hostname": true, "status": "2", "vnc": true, "serid": 9},
		"2":        map[string]any{"vpsid": "2", "status": false, "suspended": false, "cores": true},
		"3":        map[string]any{"vpsid": 3, "status": true, "suspended": "true"},
		"4":        map[string]any{"vpsid": 4, "status": 9},
		"metadata": []any{1},
	})
	page, err := p.ListServers(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, page.Servers, 4)
	for i, id := range []string{"2", "3", "4", "10"} {
		require.Equal(t, id, page.Servers[i].ExternalID)
	}
	require.Equal(t, providers.StateStopped, page.Servers[0].State)
	require.True(t, page.Servers[0].Capabilities.CanStart.Available)
	require.Equal(t, providers.StateSuspended, page.Servers[1].State)
	require.Equal(t, providers.StateUnknown, page.Servers[2].State)
	require.Equal(t, providers.StateSuspended, page.Servers[3].State)
	require.Equal(t, "9", page.Servers[3].Scope)
	require.False(t, page.Servers[3].Capabilities.CanEmbedConsole.Available)
}

func TestProviderListsRejectsMalformedNumericEntries(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `"invalid"`, `{}`, `{"vpsid":4}`, `{"vpsid":3,"cores":"bad"}`, `{"vpsid":3,"vnc":{}}`, `{"vpsid":3,"ips":42}`} {
		t.Run(raw, func(t *testing.T) {
			p := newVirtualizorFixture(t, map[string]any{"3": json.RawMessage(raw)})
			page, err := p.ListServers(context.Background(), nil)
			require.Error(t, err)
			require.Empty(t, page.Servers)
		})
	}
}

func TestProviderGetsServer(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "enabled", true: "disabled"}[disabled], func(t *testing.T) {
			p := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				if q.Get("act") != "vpsmanage" || q.Get("svs") != "3332" || len(q) != 5 {
					t.Error("unexpected lookup query")
				}
				writeJSON(t, w, map[string]any{"info": map[string]any{
					"status": "1",
					"vps":    map[string]any{"vpsid": 3332, "hostname": "edge", "vnc": !disabled, "serid": "7"},
					"ip":     map[string]any{"s1": "198.51.100.20", "s2": "2001:db8::20"}, "server_name": "node-a",
					"flags": map[string]any{"enable_console": !disabled},
				}})
			})
			server, err := p.GetServer(context.Background(), providers.ServerRef{ExternalID: "3332"})
			require.NoError(t, err)
			require.Equal(t, "node-a", server.Scope)
			require.Equal(t, providers.StateRunning, server.State)
			require.Equal(t, !disabled, server.Capabilities.CanEmbedConsole.Available)
			require.True(t, server.Capabilities.HasProviderPortal.Available)
			require.JSONEq(t, `[{"type":"ipv4","address":"198.51.100.20"},{"type":"ipv6","address":"2001:db8::20"}]`, string(server.Addresses))
		})
	}
}

func TestProviderGetsUsesAndValidatesInfoStatus(t *testing.T) {
	for _, test := range []struct {
		name      string
		status    any
		suspended bool
		state     providers.ServerState
	}{
		{"running", 1, false, providers.StateRunning},
		{"stopped string", "0", false, providers.StateStopped},
		{"suspended status", "2", false, providers.StateSuspended},
		{"suspension wins", 1, true, providers.StateSuspended},
		{"unknown", 9, false, providers.StateUnknown},
		{"missing", nil, false, ""},
		{"null", json.RawMessage(`null`), false, ""},
		{"malformed", "bad", false, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) {
				info := map[string]any{"vps": map[string]any{"vpsid": 3332, "vnc": 1, "suspended": test.suspended}}
				if test.status != nil {
					info["status"] = test.status
				}
				writeJSON(t, w, map[string]any{"info": info})
			})
			server, err := p.GetServer(context.Background(), providers.ServerRef{ExternalID: "3332"})
			if test.state == "" {
				assertSafeError(t, err, providers.ErrorProvider)
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.state, server.State)
			require.Equal(t, test.state == providers.StateRunning, server.Capabilities.CanStop.Available)
			if test.state == providers.StateSuspended {
				require.False(t, server.Capabilities.CanEmbedConsole.Available)
			}
		})
	}
}

func TestProviderGetsRejectsMismatchedID(t *testing.T) {
	p := fixtureProvider(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{"info": map[string]any{"vps": map[string]any{"vpsid": 8}}})
	})
	_, err := p.GetServer(context.Background(), providers.ServerRef{ExternalID: "3332"})
	require.Error(t, err)
}

func TestFactoryRejectsUnsafeEndpointsAndCredentials(t *testing.T) {
	for _, endpoint := range []string{"http://198.51.100.10", "https://user:secret@198.51.100.10", "https://198.51.100.10?apikey=secret", "https://198.51.100.10#secret", "https://198.51.100.10/path", "https://127.0.0.1", "https://10.2.3.4", "https://169.254.169.254", "//198.51.100.10", "https://198.51.100.10?"} {
		_, err := NewFactory(FactoryOptions{}).Create(fixtureConfig(endpoint))
		require.Error(t, err, endpoint)
		require.NotContains(t, err.Error(), "secret")
	}
	for _, raw := range []string{`{}`, `null`, `{"api_key":"key"}`, `{"api_key":"key","api_password":"pass","extra":true}`, `{} {}`} {
		config := fixtureConfig("https://198.51.100.10")
		config.Credentials = json.RawMessage(raw)
		_, err := NewFactory(FactoryOptions{}).Create(config)
		require.Error(t, err)
	}
	_, allowed, err := net.ParseCIDR("10.2.0.0/16")
	require.NoError(t, err)
	_, err = NewFactory(FactoryOptions{AllowedPrivateCIDRs: []*net.IPNet{allowed}}).Create(fixtureConfig("https://10.2.3.4/"))
	require.NoError(t, err)
}

func TestFactoryIsolatesConnectionCredentials(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		key := q.Get("apikey")
		if q.Get("api") != "json" || q.Get("act") != "listvs" || q.Get("apipass") != key+"-password" {
			t.Error("credentials crossed connection boundaries")
		}
		writeJSON(t, w, map[string]any{"1": map[string]any{"vpsid": 1, "hostname": key}})
	}))
	t.Cleanup(server.Close)
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	factory := newFactory(factoryOptions{allowLoopback: true, rootCAs: roots})
	for _, key := range []string{"one", "two"} {
		config := fixtureConfig(server.URL)
		config.ID = key
		config.Credentials = json.RawMessage(`{"api_key":"` + key + `","api_password":"` + key + `-password"}`)
		p, err := factory.Create(config)
		require.NoError(t, err)
		t.Cleanup(p.(*Provider).client.CloseIdleConnections)
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			page, err := p.ListServers(context.Background(), nil)
			require.NoError(t, err)
			require.Equal(t, key, page.Servers[0].Name)
		})
	}
}
