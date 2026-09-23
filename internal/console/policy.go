package console

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
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

type TCPDialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

type TargetPolicyOptions struct {
	AllowedPrivateCIDRs []*net.IPNet
	AllowMockTransport  bool
	Dialer              TCPDialer
	RootCAs             *x509.CertPool
}

type TargetPolicy struct {
	resolver Resolver
	options  TargetPolicyOptions
}

func NewTargetPolicy(resolver Resolver, options TargetPolicyOptions) *TargetPolicy {
	if resolver == nil {
		resolver = netResolver{resolver: net.DefaultResolver}
	}
	if options.Dialer == nil {
		options.Dialer = &net.Dialer{Timeout: 10 * time.Second}
	}
	return &TargetPolicy{resolver: resolver, options: options}
}

func (policy *TargetPolicy) ValidateEmbedded(ctx context.Context, target *url.URL) error {
	if target != nil && target.Scheme == "vnc+tcp" {
		if err := validateRawTarget(target); err != nil {
			return err
		}
		return policy.validateNetworkTarget(ctx, target)
	}
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
	_, err := policy.resolveAllowed(ctx, target.Hostname())
	return err
}

func (policy *TargetPolicy) resolveAllowed(ctx context.Context, host string) ([]net.IP, error) {
	addresses := make([]net.IP, 0, 1)
	if literal := net.ParseIP(host); literal != nil {
		addresses = append(addresses, literal)
	} else {
		resolved, err := policy.resolver.LookupIP(ctx, host)
		if err != nil || len(resolved) == 0 {
			return nil, ErrTargetRejected
		}
		addresses = append(addresses, resolved...)
	}
	for _, address := range addresses {
		if !policy.addressAllowed(address) {
			return nil, ErrTargetRejected
		}
	}
	return addresses, nil
}

func validateRawTarget(target *url.URL) error {
	if validateURLShape(target) != nil || target.Scheme != "vnc+tcp" || target.Path != "" || target.RawPath != "" || target.RawQuery != "" || target.ForceQuery || target.Opaque != "" || target.RawFragment != "" {
		return ErrTargetRejected
	}
	host, port, err := net.SplitHostPort(target.Host)
	if err != nil || strings.TrimSpace(host) == "" || port == "" {
		return ErrTargetRejected
	}
	for _, digit := range port {
		if digit < '0' || digit > '9' {
			return ErrTargetRejected
		}
	}
	value, err := strconv.Atoi(port)
	if err != nil || value < 1 || value > 65535 {
		return ErrTargetRejected
	}
	return nil
}

// DialTCP rechecks every DNS answer and dials only approved literal addresses.
func (policy *TargetPolicy) DialTCP(ctx context.Context, target *url.URL) (net.Conn, error) {
	if err := validateRawTarget(target); err != nil {
		return nil, err
	}
	return policy.dialAllowed(ctx, target.Hostname(), target.Port())
}

func (policy *TargetPolicy) dialAllowed(ctx context.Context, host, port string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	addresses, err := policy.resolveAllowed(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, address := range addresses {
		if ctx.Err() != nil {
			break
		}
		connection, err := policy.options.Dialer.DialContext(ctx, "tcp", net.JoinHostPort(address.String(), port))
		if err == nil {
			return connection, nil
		}
	}
	return nil, ErrTargetUnavailable
}

// webSocketClient keeps the TLS identity and HTTP origin intact while resolving
// again at the actual TCP dial and connecting only to policy-approved literals.
func (policy *TargetPolicy) webSocketClient(ctx context.Context, target *url.URL) (*http.Client, error) {
	if policy == nil || target == nil || target.Scheme != "wss" || target.Opaque != "" {
		return nil, ErrTargetRejected
	}
	if err := policy.ValidateEmbedded(ctx, target); err != nil {
		return nil, err
	}
	port := target.Port()
	if port == "" {
		port = "443"
	}
	value, err := strconv.Atoi(port)
	if err != nil || value < 1 || value > 65535 || strconv.Itoa(value) != port {
		return nil, ErrTargetRejected
	}
	transport := &http.Transport{
		// Proxy intentionally remains nil: an environment proxy must not resolve
		// the host again or bypass the address policy applied by DialContext.
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, dialPort, err := net.SplitHostPort(address)
			if err != nil || !strings.EqualFold(host, target.Hostname()) || dialPort != port {
				return nil, ErrTargetRejected
			}
			return policy.dialAllowed(ctx, host, dialPort)
		},
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: policy.options.RootCAs},
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		DisableKeepAlives:     true,
	}
	return &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return ErrTargetRejected },
	}, nil
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
