package console

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
)

var ErrTargetRejected = errors.New("console target rejected by security policy")

type Resolver interface {
	LookupIP(context.Context, string) ([]net.IP, error)
}

type netResolver struct{ resolver *net.Resolver }

func (resolver netResolver) LookupIP(ctx context.Context, host string) ([]net.IP, error) {
	addresses, err := resolver.resolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	return addresses, nil
}

type TargetPolicyOptions struct {
	AllowedPrivateCIDRs []*net.IPNet
	AllowMockTransport  bool
}

type TargetPolicy struct {
	resolver Resolver
	options  TargetPolicyOptions
}

func NewTargetPolicy(resolver Resolver, options TargetPolicyOptions) *TargetPolicy {
	if resolver == nil {
		resolver = netResolver{resolver: net.DefaultResolver}
	}
	return &TargetPolicy{resolver: resolver, options: options}
}

func (policy *TargetPolicy) ValidateEmbedded(ctx context.Context, target *url.URL) error {
	if target != nil && target.Scheme == "mock+ws" && policy.options.AllowMockTransport && target.Hostname() == "console" {
		return validateURLShape(target)
	}
	if target == nil || target.Scheme != "wss" {
		return ErrTargetRejected
	}
	return policy.validateNetworkTarget(ctx, target)
}

func (policy *TargetPolicy) ValidateExternal(ctx context.Context, target *url.URL) error {
	if target == nil || target.Scheme != "https" {
		return ErrTargetRejected
	}
	return policy.validateNetworkTarget(ctx, target)
}

func (policy *TargetPolicy) validateNetworkTarget(ctx context.Context, target *url.URL) error {
	if err := validateURLShape(target); err != nil {
		return err
	}
	host := target.Hostname()
	addresses := make([]net.IP, 0, 1)
	if literal := net.ParseIP(host); literal != nil {
		addresses = append(addresses, literal)
	} else {
		resolved, err := policy.resolver.LookupIP(ctx, host)
		if err != nil || len(resolved) == 0 {
			return ErrTargetRejected
		}
		addresses = append(addresses, resolved...)
	}
	for _, address := range addresses {
		if !policy.addressAllowed(address) {
			return ErrTargetRejected
		}
	}
	return nil
}

func validateURLShape(target *url.URL) error {
	if target == nil || strings.TrimSpace(target.Hostname()) == "" || target.User != nil || target.Fragment != "" {
		return ErrTargetRejected
	}
	return nil
}

func (policy *TargetPolicy) addressAllowed(address net.IP) bool {
	if address == nil || address.IsUnspecified() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.IsMulticast() {
		return false
	}
	if !address.IsPrivate() {
		return true
	}
	for _, allowed := range policy.options.AllowedPrivateCIDRs {
		if allowed != nil && allowed.Contains(address) {
			return true
		}
	}
	return false
}
