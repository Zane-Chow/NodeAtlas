package console

import (
	"context"
	"errors"
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type staticResolver map[string][]net.IP

func (resolver staticResolver) LookupIP(_ context.Context, host string) ([]net.IP, error) {
	return resolver[host], nil
}

func TestTargetPolicyAcceptsPublicTargetsAndApprovedPrivateCIDR(t *testing.T) {
	_, privateCIDR, err := net.ParseCIDR("10.20.0.0/16")
	require.NoError(t, err)
	policy := NewTargetPolicy(staticResolver{
		"console.example.test": {net.ParseIP("203.0.113.10")},
		"panel.internal.test":  {net.ParseIP("10.20.1.5")},
	}, TargetPolicyOptions{AllowedPrivateCIDRs: []*net.IPNet{privateCIDR}})

	require.NoError(t, policy.ValidateEmbedded(context.Background(), mustURL(t, "wss://console.example.test/session?token=temporary")))
	require.NoError(t, policy.ValidateExternal(context.Background(), mustURL(t, "https://panel.internal.test/server/1")))
}

func TestTargetPolicyRejectsUnsafeTargets(t *testing.T) {
	policy := NewTargetPolicy(staticResolver{
		"loopback.test": {net.ParseIP("127.0.0.1")},
		"metadata.test": {net.ParseIP("169.254.169.254")},
		"private.test":  {net.ParseIP("10.0.0.10")},
		"public.test":   {net.ParseIP("203.0.113.10")},
	}, TargetPolicyOptions{})

	tests := []struct {
		name, raw string
		embedded  bool
	}{
		{"embedded HTTP", "http://public.test/console", true},
		{"external HTTP", "http://public.test/portal", false},
		{"userinfo", "https://user:secret@public.test/portal", false},
		{"fragment", "https://public.test/portal#secret", false},
		{"loopback", "wss://loopback.test/console", true},
		{"metadata", "wss://metadata.test/console", true},
		{"private", "https://private.test/portal", false},
		{"IP literal", "wss://127.0.0.1/console", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var err error
			if test.embedded {
				err = policy.ValidateEmbedded(context.Background(), mustURL(t, test.raw))
			} else {
				err = policy.ValidateExternal(context.Background(), mustURL(t, test.raw))
			}
			require.ErrorIs(t, err, ErrTargetRejected)
		})
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	require.NoError(t, err)
	return parsed
}

type tcpDialerFunc func(context.Context, string, string) (net.Conn, error)

func (dial tcpDialerFunc) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return dial(ctx, network, address)
}

type resolverFunc func(context.Context, string) ([]net.IP, error)

func (resolve resolverFunc) LookupIP(ctx context.Context, host string) ([]net.IP, error) {
	return resolve(ctx, host)
}

func TestTargetPolicyTCPShape(t *testing.T) {
	policy := NewTargetPolicy(staticResolver{"public.test": {net.ParseIP("203.0.113.10")}}, TargetPolicyOptions{})
	for _, raw := range []string{"vnc+tcp://public.test:1", "vnc+tcp://public.test:65535", "vnc+tcp://[2001:db8::1]:5951"} {
		require.NoError(t, policy.ValidateEmbedded(context.Background(), mustURL(t, raw)))
	}
	for _, target := range []*url.URL{
		nil,
		{Scheme: "tcp", Host: "public.test:5951"},
		{Scheme: "vnc+tcp", Host: ":5951"},
		{Scheme: "vnc+tcp", Host: "public.test"},
		{Scheme: "vnc+tcp", Host: "public.test:0"},
		{Scheme: "vnc+tcp", Host: "public.test:65536"},
		{Scheme: "vnc+tcp", Host: "public.test:abc"},
		{Scheme: "vnc+tcp", Host: "public.test:+1"},
		{Scheme: "vnc+tcp", Host: "public.test:5951", User: url.User("secret")},
		{Scheme: "vnc+tcp", Host: "public.test:5951", Path: "/"},
		{Scheme: "vnc+tcp", Host: "public.test:5951", RawPath: "/secret"},
		{Scheme: "vnc+tcp", Host: "public.test:5951", RawQuery: "secret"},
		{Scheme: "vnc+tcp", Host: "public.test:5951", ForceQuery: true},
		{Scheme: "vnc+tcp", Host: "public.test:5951", Fragment: "secret"},
		{Scheme: "vnc+tcp", Host: "public.test:5951", Opaque: "secret"},
	} {
		_, err := policy.DialTCP(context.Background(), target)
		require.ErrorIs(t, err, ErrTargetRejected, "%+v", target)
		if target != nil && target.Scheme == "vnc+tcp" {
			require.ErrorIs(t, policy.ValidateEmbedded(context.Background(), target), ErrTargetRejected)
		}
	}
}

func TestTargetPolicyTCPChecksFreshDNSAndPinsSequentialDials(t *testing.T) {
	addresses := []net.IP{net.ParseIP("203.0.113.10")}
	lookups := 0
	var dialed []string
	proxy, peer := net.Pipe()
	t.Cleanup(func() { _ = proxy.Close(); _ = peer.Close() })
	policy := NewTargetPolicy(resolverFunc(func(_ context.Context, host string) ([]net.IP, error) {
		require.Equal(t, "public.test", host)
		lookups++
		return addresses, nil
	}), TargetPolicyOptions{Dialer: tcpDialerFunc(func(ctx context.Context, network, address string) (net.Conn, error) {
		require.Equal(t, "tcp", network)
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		require.InDelta(t, 10, time.Until(deadline).Seconds(), 1)
		dialed = append(dialed, address)
		if len(dialed) == 1 {
			return nil, errors.New("sensitive target public.test")
		}
		return proxy, nil
	})})
	target := mustURL(t, "vnc+tcp://public.test:5951")
	require.NoError(t, policy.ValidateEmbedded(context.Background(), target))
	addresses = []net.IP{net.ParseIP("203.0.113.11"), net.ParseIP("2001:db8::1")}
	connection, err := policy.DialTCP(context.Background(), target)
	require.NoError(t, err)
	require.Same(t, proxy, connection)
	require.Equal(t, 2, lookups)
	require.Equal(t, []string{"203.0.113.11:5951", "[2001:db8::1]:5951"}, dialed)
}

func TestTargetPolicyTCPRejectsAnyBlockedDNSAnswerBeforeDial(t *testing.T) {
	for _, blocked := range []string{"127.0.0.1", "169.254.169.254", "10.0.0.1", "::", "ff02::1"} {
		t.Run(blocked, func(t *testing.T) {
			addresses := []net.IP{net.ParseIP("203.0.113.10")}
			policy := NewTargetPolicy(resolverFunc(func(context.Context, string) ([]net.IP, error) { return addresses, nil }), TargetPolicyOptions{
				Dialer: tcpDialerFunc(func(context.Context, string, string) (net.Conn, error) {
					t.Fatal("dialed blocked DNS answer set")
					return nil, nil
				}),
			})
			target := mustURL(t, "vnc+tcp://public.test:5951")
			require.NoError(t, policy.ValidateEmbedded(context.Background(), target))
			addresses = append(addresses, net.ParseIP(blocked))
			_, err := policy.DialTCP(context.Background(), target)
			require.ErrorIs(t, err, ErrTargetRejected)
		})
	}
}

func TestTargetPolicyTCPRedactsDialAndDNSErrors(t *testing.T) {
	for _, dnsFailure := range []bool{false, true} {
		policy := NewTargetPolicy(resolverFunc(func(context.Context, string) ([]net.IP, error) {
			if dnsFailure {
				return nil, errors.New("sensitive DNS target")
			}
			return []net.IP{net.ParseIP("203.0.113.10")}, nil
		}), TargetPolicyOptions{Dialer: tcpDialerFunc(func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("sensitive dial target")
		})})
		_, err := policy.DialTCP(context.Background(), mustURL(t, "vnc+tcp://public.test:5951"))
		require.Error(t, err)
		require.NotContains(t, err.Error(), "sensitive")
		require.NotContains(t, err.Error(), "public.test")
		require.NotContains(t, err.Error(), "203.0.113.10")
	}
}
