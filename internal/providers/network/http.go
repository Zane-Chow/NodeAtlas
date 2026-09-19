package network

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/url"
	"time"
)

type HTTPOptions struct {
	AllowHTTP bool
	Timeout   time.Duration
	RootCAs   *x509.CertPool
}

func NewHTTPClient(policy Policy, origin *url.URL, options HTTPOptions) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, errors.New("invalid provider network address")
			}
			addresses, err := policy.ResolveAllowed(ctx, host)
			if err != nil {
				return nil, err
			}
			var lastErr error
			for _, resolved := range addresses {
				connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(resolved.String(), port))
				if dialErr == nil {
					return connection, nil
				}
				lastErr = dialErr
			}
			if lastErr == nil {
				return nil, errors.New("provider network host resolved without addresses")
			}
			return nil, lastErr
		},
		ForceAttemptHTTP2: true,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			RootCAs:    options.RootCAs,
		},
		MaxIdleConns:        20,
		IdleConnTimeout:     60 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	timeout := options.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(request *http.Request, _ []*http.Request) error {
			if request.URL.Scheme != "https" && !(options.AllowHTTP && request.URL.Scheme == "http") {
				return errors.New("provider redirect changed protocol")
			}
			if !SameOrigin(request.URL, origin) {
				return errors.New("provider cross-origin redirect rejected")
			}
			return policy.Validate(request.Context(), request.URL)
		},
	}
}
