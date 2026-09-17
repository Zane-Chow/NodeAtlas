package console

import (
	"context"
	"net"
	"net/url"
	"testing"

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
