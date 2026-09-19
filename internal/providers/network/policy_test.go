package network

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

type staticResolver map[string][]net.IP

func (resolver staticResolver) LookupIP(_ context.Context, _ string, host string) ([]net.IP, error) {
	return resolver[host], nil
}

type rebindingResolver struct {
	mutex     sync.Mutex
	addresses [][]net.IP
}

type countingResolver struct {
	addresses []net.IP
	calls     atomic.Int32
}

func (resolver *countingResolver) LookupIP(context.Context, string, string) ([]net.IP, error) {
	resolver.calls.Add(1)
	return resolver.addresses, nil
}

func (resolver *rebindingResolver) LookupIP(context.Context, string, string) ([]net.IP, error) {
	resolver.mutex.Lock()
	defer resolver.mutex.Unlock()
	if len(resolver.addresses) == 0 {
		return nil, errors.New("unexpected lookup")
	}
	addresses := resolver.addresses[0]
	resolver.addresses = resolver.addresses[1:]
	return addresses, nil
}

func TestPolicyAllowsPublicAndExplicitlyAllowedPrivateAddresses(t *testing.T) {
	_, privateNetwork, err := net.ParseCIDR("10.20.0.0/16")
	require.NoError(t, err)

	tests := []struct {
		name    string
		host    string
		address net.IP
		options PolicyOptions
	}{
		{name: "public IPv4", host: "public.example.test", address: net.ParseIP("203.0.113.10")},
		{name: "public IPv6", host: "public-v6.example.test", address: net.ParseIP("2001:db8::10")},
		{
			name:    "explicitly allowed private address",
			host:    "private.example.test",
			address: net.ParseIP("10.20.30.40"),
			options: PolicyOptions{AllowedPrivateCIDRs: []*net.IPNet{privateNetwork}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := NewPolicy(staticResolver{test.host: {test.address}}, test.options)

			require.NoError(t, policy.Validate(context.Background(), mustURL(t, "https://"+test.host)))
		})
	}
}

func TestPolicyRejectsBlockedAddresses(t *testing.T) {
	tests := []struct {
		name    string
		address string
	}{
		{name: "loopback", address: "127.0.0.1"},
		{name: "link-local unicast", address: "169.254.1.1"},
		{name: "link-local multicast", address: "224.0.0.1"},
		{name: "unspecified", address: "0.0.0.0"},
		{name: "multicast", address: "239.1.2.3"},
		{name: "unapproved private", address: "10.20.30.40"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := NewPolicy(staticResolver{
				"blocked.example.test": {net.ParseIP(test.address)},
			}, PolicyOptions{})

			err := policy.Validate(context.Background(), mustURL(t, "https://blocked.example.test"))
			require.ErrorIs(t, err, ErrAddressRejected)
		})
	}
}

func TestPolicyRejectsAnyBlockedResolvedAddress(t *testing.T) {
	policy := NewPolicy(staticResolver{
		"mixed.example.test": {net.ParseIP("203.0.113.10"), net.ParseIP("10.0.0.7")},
	}, PolicyOptions{})

	err := policy.Validate(context.Background(), mustURL(t, "https://mixed.example.test"))
	require.ErrorIs(t, err, ErrAddressRejected)
}

func TestPolicyResolveAllowedSupportsIPLiteral(t *testing.T) {
	policy := NewPolicy(staticResolver{}, PolicyOptions{})

	addresses, err := policy.ResolveAllowed(context.Background(), "203.0.113.10")
	require.NoError(t, err)
	require.Equal(t, []net.IP{net.ParseIP("203.0.113.10")}, addresses)
}

func TestHTTPClientAllowsSameOriginHTTPSRedirect(t *testing.T) {
	policy := NewPolicy(staticResolver{
		"origin.example.test": {net.ParseIP("203.0.113.10")},
	}, PolicyOptions{})
	origin := mustURL(t, "https://origin.example.test")
	client := NewHTTPClient(policy, origin, HTTPOptions{})

	request := &http.Request{URL: mustURL(t, "https://origin.example.test/next")}
	require.NoError(t, client.CheckRedirect(request, nil))
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

func TestHTTPClientRequiresHTTPSForInitialRequestUnlessExplicitlyAllowed(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		response.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	serverURL := mustURL(t, server.URL)
	origin := mustURL(t, "http://fixture.example.test:"+serverURL.Port())
	resolver := &countingResolver{addresses: []net.IP{net.ParseIP("127.0.0.1")}}
	policy := NewPolicy(resolver, PolicyOptions{AllowLoopback: true})

	client := NewHTTPClient(policy, origin, HTTPOptions{})
	response, err := client.Get(origin.String())
	if response != nil {
		response.Body.Close()
	}
	require.Error(t, err)
	require.Zero(t, resolver.calls.Load(), "HTTP rejection must happen before resolution or dialing")
	require.Zero(t, requests.Load())

	client = NewHTTPClient(policy, origin, HTTPOptions{AllowHTTP: true})
	response, err = client.Get(origin.String())
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, int32(1), resolver.calls.Load())
	require.Equal(t, int32(1), requests.Load())
}

func TestHTTPClientRejectsDNSRebindingAtDial(t *testing.T) {
	resolver := &rebindingResolver{addresses: [][]net.IP{
		{net.ParseIP("203.0.113.10")},
		{net.ParseIP("127.0.0.1")},
	}}
	policy := NewPolicy(resolver, PolicyOptions{})
	origin := mustURL(t, "http://rebind.example.test")
	require.NoError(t, policy.Validate(context.Background(), origin))
	client := NewHTTPClient(policy, origin, HTTPOptions{AllowHTTP: true})

	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, origin.String(), nil)
	require.NoError(t, err)
	_, err = client.Do(request)
	require.ErrorIs(t, err, ErrAddressRejected)
}

func TestSameOriginUsesEffectivePorts(t *testing.T) {
	require.True(t, SameOrigin(mustURL(t, "https://example.test/path"), mustURL(t, "https://EXAMPLE.test:443/other")))
	require.False(t, SameOrigin(mustURL(t, "https://example.test"), mustURL(t, "https://example.test:8443")))
	require.False(t, SameOrigin(mustURL(t, "https://example.test"), mustURL(t, "http://example.test")))
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	require.NoError(t, err)
	return parsed
}
