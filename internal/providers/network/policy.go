package network

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

var ErrAddressRejected = errors.New("provider network address rejected")

type Resolver interface {
	LookupIP(context.Context, string, string) ([]net.IP, error)
}

type PolicyOptions struct {
	AllowedPrivateCIDRs []*net.IPNet
	AllowLoopback       bool
}

type Policy struct {
	resolver            Resolver
	allowedPrivateCIDRs []*net.IPNet
	allowLoopback       bool
}

func NewPolicy(resolver Resolver, options PolicyOptions) Policy {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	return Policy{
		resolver:            resolver,
		allowedPrivateCIDRs: options.AllowedPrivateCIDRs,
		allowLoopback:       options.AllowLoopback,
	}
}

func (policy Policy) ResolveAllowed(ctx context.Context, host string) ([]net.IP, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return nil, errors.New("provider network host is required")
	}

	var addresses []net.IP
	if literal := net.ParseIP(host); literal != nil {
		addresses = []net.IP{literal}
	} else {
		var err error
		addresses, err = policy.resolver.LookupIP(ctx, "ip", host)
		if err != nil {
			return nil, fmt.Errorf("resolve provider network host: %w", err)
		}
	}
	if len(addresses) == 0 {
		return nil, errors.New("provider network host resolved without addresses")
	}
	for _, address := range addresses {
		if !policy.addressAllowed(address) {
			return nil, ErrAddressRejected
		}
	}
	return addresses, nil
}

func (policy Policy) Validate(ctx context.Context, target *url.URL) error {
	if target == nil || target.Hostname() == "" {
		return errors.New("provider network URL must include a host")
	}
	_, err := policy.ResolveAllowed(ctx, target.Hostname())
	return err
}

func (policy Policy) addressAllowed(address net.IP) bool {
	if address == nil || address.IsUnspecified() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.IsMulticast() {
		return false
	}
	if address.IsLoopback() {
		return policy.allowLoopback
	}
	if !address.IsPrivate() {
		return true
	}
	for _, allowed := range policy.allowedPrivateCIDRs {
		if allowed != nil && allowed.Contains(address) {
			return true
		}
	}
	return false
}

func SameOrigin(left, right *url.URL) bool {
	if left == nil || right == nil {
		return false
	}
	return strings.EqualFold(left.Scheme, right.Scheme) &&
		strings.EqualFold(left.Hostname(), right.Hostname()) &&
		effectivePort(left) == effectivePort(right)
}

func effectivePort(target *url.URL) string {
	if target.Port() != "" {
		return target.Port()
	}
	switch strings.ToLower(target.Scheme) {
	case "https", "wss":
		return "443"
	case "http", "ws":
		return "80"
	default:
		return ""
	}
}
