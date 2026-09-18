package virtfusion

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"controlpanel/internal/providers"
)

const maxResponseBytes = 4 << 20

type Resolver interface {
	LookupIP(context.Context, string, string) ([]net.IP, error)
}

type FactoryOptions struct {
	AllowedPrivateCIDRs []*net.IPNet
}

type factoryOptions struct {
	allowedPrivateCIDRs []*net.IPNet
	resolver            Resolver
	allowHTTP           bool
	allowLoopback       bool
}

type Factory struct{ options factoryOptions }

type Provider struct {
	apiBase   *url.URL
	portalURL *url.URL
	token     string
	pageSize  int
	client    *http.Client
}

type settings struct {
	PageSize int `json:"page_size,omitempty"`
}

type credentials struct {
	Token string `json:"token"`
}

type pageCursor struct {
	Page int `json:"page"`
}

type listResponse struct {
	CurrentPage int `json:"current_page"`
	LastPage    int `json:"last_page"`
	Data        []struct {
		ID json.Number `json:"id"`
	} `json:"data"`
}

type detailResponse struct {
	Data serverData `json:"data"`
}

type serverData struct {
	ID               json.Number     `json:"id"`
	Name             string          `json:"name"`
	HypervisorID     json.Number     `json:"hypervisorId"`
	State            string          `json:"state"`
	CommissionStatus int             `json:"commissionStatus"`
	Suspended        bool            `json:"suspended"`
	BuildFailed      bool            `json:"buildFailed"`
	RemoteState      json.RawMessage `json:"remoteState"`
	Settings         struct {
		Resources struct {
			Memory   int `json:"memory"`
			Storage  int `json:"storage"`
			Traffic  int `json:"traffic"`
			CPUCores int `json:"cpuCores"`
		} `json:"resources"`
	} `json:"settings"`
	VNC *struct {
		Enabled bool `json:"enabled"`
	} `json:"vnc"`
	Network struct {
		Interfaces []struct {
			IPv4 []struct {
				Address string `json:"address"`
			} `json:"ipv4"`
			IPv6 []struct {
				Address string `json:"address"`
			} `json:"ipv6"`
		} `json:"interfaces"`
	} `json:"network"`
}

type actionResponse struct {
	Data struct {
		QueueID json.Number `json:"queueId"`
	} `json:"data"`
}

type vncResponse struct {
	Data struct {
		VNC struct {
			Password string `json:"password"`
			WSS      struct {
				URL string `json:"url"`
			} `json:"wss"`
		} `json:"vnc"`
	} `json:"data"`
}

func NewFactory(options FactoryOptions) *Factory {
	return newFactory(factoryOptions{allowedPrivateCIDRs: options.AllowedPrivateCIDRs})
}

func newFactory(options factoryOptions) *Factory {
	if options.resolver == nil {
		options.resolver = net.DefaultResolver
	}
	return &Factory{options: options}
}

func (factory *Factory) Create(config providers.ConnectionConfig) (providers.Provider, error) {
	if factory == nil || strings.TrimSpace(config.ID) == "" {
		return nil, errors.New("VirtFusion connection ID is required")
	}
	var configuration settings
	if len(config.Settings) == 0 {
		config.Settings = json.RawMessage(`{}`)
	}
	if err := decodeStrict(config.Settings, &configuration); err != nil {
		return nil, errors.New("invalid VirtFusion settings")
	}
	if configuration.PageSize == 0 {
		configuration.PageSize = 200
	}
	if configuration.PageSize < 1 || configuration.PageSize > 200 {
		return nil, errors.New("VirtFusion page_size must be between 1 and 200")
	}
	var secret credentials
	if err := decodeStrict(config.Credentials, &secret); err != nil || strings.TrimSpace(secret.Token) == "" {
		return nil, errors.New("invalid VirtFusion credentials")
	}
	portalURL, apiBase, err := normalizeEndpoint(config.Endpoint, factory.options.allowHTTP)
	if err != nil {
		return nil, err
	}
	policy := endpointPolicy{resolver: factory.options.resolver, allowedPrivateCIDRs: factory.options.allowedPrivateCIDRs, allowLoopback: factory.options.allowLoopback}
	validationContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := policy.validate(validationContext, portalURL); err != nil {
		return nil, errors.New("VirtFusion endpoint is not allowed")
	}
	client := newHTTPClient(policy, portalURL, factory.options.allowHTTP)
	return &Provider{apiBase: apiBase, portalURL: portalURL, token: strings.TrimSpace(secret.Token), pageSize: configuration.PageSize, client: client}, nil
}

func (provider *Provider) ValidateConnection(ctx context.Context) (providers.ConnectionInfo, error) {
	if err := provider.request(ctx, http.MethodGet, "/connect", nil, nil); err != nil {
		return providers.ConnectionInfo{}, err
	}
	return providers.ConnectionInfo{DisplayName: "VirtFusion", Version: "API v1"}, nil
}

func (provider *Provider) ListServers(ctx context.Context, cursor *providers.Cursor) (providers.ServerPage, error) {
	page, err := decodeCursor(cursor)
	if err != nil {
		return providers.ServerPage{}, &providers.Error{Code: providers.ErrorInvalidConfig, Message: "invalid VirtFusion inventory cursor"}
	}
	query := url.Values{"type": {"simple"}, "results": {strconv.Itoa(provider.pageSize)}, "page": {strconv.Itoa(page)}}
	var listed listResponse
	if err := provider.request(ctx, http.MethodGet, "/servers?"+query.Encode(), nil, &listed); err != nil {
		return providers.ServerPage{}, err
	}
	if listed.CurrentPage != page || listed.LastPage < listed.CurrentPage || listed.LastPage < 1 || listed.LastPage > 1_000_000 || (listed.CurrentPage < listed.LastPage && len(listed.Data) == 0) {
		return providers.ServerPage{}, &providers.Error{Code: providers.ErrorProvider, Message: "VirtFusion returned invalid pagination data"}
	}
	result := providers.ServerPage{Servers: make([]providers.RemoteServer, 0, len(listed.Data))}
	for _, item := range listed.Data {
		id, err := normalizeServerID(item.ID.String())
		if err != nil {
			return providers.ServerPage{}, &providers.Error{Code: providers.ErrorProvider, Message: "VirtFusion returned an invalid server ID"}
		}
		server, err := provider.getServer(ctx, id)
		if err != nil {
			return providers.ServerPage{}, err
		}
		result.Servers = append(result.Servers, server)
	}
	if page < listed.LastPage {
		result.Next = &providers.Cursor{Value: encodeCursor(page + 1)}
	}
	return result, nil
}

func (provider *Provider) GetServer(ctx context.Context, ref providers.ServerRef) (providers.RemoteServer, error) {
	id, err := normalizeServerID(ref.ExternalID)
	if err != nil {
		return providers.RemoteServer{}, &providers.Error{Code: providers.ErrorNotFound, Message: "VirtFusion server was not found"}
	}
	return provider.getServer(ctx, id)
}

func (provider *Provider) getServer(ctx context.Context, id string) (providers.RemoteServer, error) {
	var response detailResponse
	if err := provider.request(ctx, http.MethodGet, "/servers/"+id+"?remoteState=true", nil, &response); err != nil {
		return providers.RemoteServer{}, err
	}
	if _, err := normalizeServerID(response.Data.ID.String()); err != nil || response.Data.ID.String() != id {
		return providers.RemoteServer{}, &providers.Error{Code: providers.ErrorProvider, Message: "VirtFusion returned mismatched server data"}
	}
	return normalizeServer(response.Data), nil
}

func (provider *Provider) StartServer(ctx context.Context, ref providers.ServerRef) (providers.ActionReceipt, error) {
	return provider.power(ctx, ref, "boot")
}

func (provider *Provider) StopServer(ctx context.Context, ref providers.ServerRef) (providers.ActionReceipt, error) {
	return provider.power(ctx, ref, "shutdown")
}

func (provider *Provider) RebootServer(ctx context.Context, ref providers.ServerRef) (providers.ActionReceipt, error) {
	return provider.power(ctx, ref, "restart")
}

func (provider *Provider) power(ctx context.Context, ref providers.ServerRef, action string) (providers.ActionReceipt, error) {
	id, err := normalizeServerID(ref.ExternalID)
	if err != nil {
		return providers.ActionReceipt{}, &providers.Error{Code: providers.ErrorNotFound, Message: "VirtFusion server was not found"}
	}
	var response actionResponse
	if err := provider.request(ctx, http.MethodPost, "/servers/"+id+"/power/"+action, nil, &response); err != nil {
		return providers.ActionReceipt{}, err
	}
	return providers.ActionReceipt{RequestID: response.Data.QueueID.String()}, nil
}

func (provider *Provider) OpenConsole(ctx context.Context, ref providers.ServerRef, mode providers.ConsoleMode) (providers.ConsoleTarget, error) {
	if mode != providers.ConsoleEmbedded {
		return providers.ConsoleTarget{}, &providers.Error{Code: providers.ErrorUnsupported, Message: "VirtFusion VNC window mode is not available yet; use embedded VNC or the provider portal"}
	}
	id, err := normalizeServerID(ref.ExternalID)
	if err != nil {
		return providers.ConsoleTarget{}, &providers.Error{Code: providers.ErrorNotFound, Message: "VirtFusion server was not found"}
	}
	var response vncResponse
	if err := provider.request(ctx, http.MethodGet, "/servers/"+id+"/vnc", nil, &response); err != nil {
		return providers.ConsoleTarget{}, err
	}
	target, err := provider.resolveVNC(response.Data.VNC.WSS.URL)
	if err != nil {
		return providers.ConsoleTarget{}, err
	}
	return providers.ConsoleTarget{Mode: mode, Protocol: "rfb", URL: target, Password: response.Data.VNC.Password}, nil
}

func (provider *Provider) ProviderPortalURL(context.Context, providers.ServerRef) (*url.URL, error) {
	copy := *provider.portalURL
	return &copy, nil
}

func (provider *Provider) resolveVNC(raw string) (*url.URL, error) {
	reference, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || reference.String() == "" || reference.User != nil || reference.Fragment != "" {
		return nil, &providers.Error{Code: providers.ErrorProvider, Message: "VirtFusion returned an invalid VNC endpoint"}
	}
	var target *url.URL
	if reference.IsAbs() {
		target = reference
	} else {
		target = provider.portalURL.ResolveReference(reference)
	}
	expectedScheme := "wss"
	if provider.portalURL.Scheme == "http" {
		expectedScheme = "ws"
	}
	if target.Scheme == provider.portalURL.Scheme {
		target.Scheme = expectedScheme
	}
	if target.Scheme != expectedScheme || !sameHost(target, provider.portalURL) || strings.TrimSpace(target.Path) == "" {
		return nil, &providers.Error{Code: providers.ErrorProvider, Message: "VirtFusion returned an untrusted VNC endpoint"}
	}
	return target, nil
}

func normalizeServer(data serverData) providers.RemoteServer {
	id := data.ID.String()
	name := strings.TrimSpace(data.Name)
	if name == "" {
		name = "VirtFusion server " + id
	}
	remoteState := decodeRemoteState(data.RemoteState)
	state := mapState(remoteState, data)
	spec, _ := json.Marshal(map[string]int{
		"memory_mb":  data.Settings.Resources.Memory,
		"storage_gb": data.Settings.Resources.Storage,
		"traffic_gb": data.Settings.Resources.Traffic,
		"cpu":        data.Settings.Resources.CPUCores,
	})
	addresses := make([]map[string]string, 0)
	for _, networkInterface := range data.Network.Interfaces {
		for _, address := range networkInterface.IPv4 {
			if value := strings.TrimSpace(address.Address); value != "" {
				addresses = append(addresses, map[string]string{"type": "ipv4", "address": value})
			}
		}
		for _, address := range networkInterface.IPv6 {
			if value := strings.TrimSpace(address.Address); value != "" {
				addresses = append(addresses, map[string]string{"type": "ipv6", "address": value})
			}
		}
	}
	encodedAddresses, _ := json.Marshal(addresses)
	vncAvailable := data.VNC != nil && !data.Suspended && !data.BuildFailed && data.CommissionStatus >= 3
	capabilities := providers.Capabilities{
		CanStart:          capability(state == providers.StateStopped, "server must be stopped"),
		CanStop:           capability(state == providers.StateRunning, "server must be running"),
		CanReboot:         capability(state == providers.StateRunning, "server must be running"),
		CanEmbedConsole:   capability(vncAvailable, "VirtFusion VNC is unavailable for this server"),
		CanOpenConsoleWindow: capability(vncAvailable, "VirtFusion VNC is unavailable for this server"),
		HasProviderPortal: providers.Capability{Available: true},
	}
	return providers.RemoteServer{ExternalID: id, Scope: data.HypervisorID.String(), Name: name, State: state, RemoteState: remoteState, Spec: spec, Addresses: encodedAddresses, Capabilities: capabilities}
}

func decodeRemoteState(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" || string(raw) == "false" {
		return "unknown"
	}
	var value string
	if json.Unmarshal(raw, &value) == nil && strings.TrimSpace(value) != "" {
		return strings.ToLower(strings.TrimSpace(value))
	}
	return "unknown"
}

func mapState(remote string, data serverData) providers.ServerState {
	if data.BuildFailed {
		return providers.StateError
	}
	if data.Suspended {
		return providers.StateSuspended
	}
	switch remote {
	case "running", "on":
		return providers.StateRunning
	case "shutoff", "stopped", "off":
		return providers.StateStopped
	case "shutdown", "stopping":
		return providers.StateStopping
	case "reboot", "rebooting":
		return providers.StateRebooting
	case "paused", "suspended":
		return providers.StateSuspended
	}
	if data.CommissionStatus < 3 || !strings.EqualFold(data.State, "complete") {
		return providers.StatePending
	}
	return providers.StateUnknown
}

func capability(available bool, reason string) providers.Capability {
	if available {
		return providers.Capability{Available: true}
	}
	return providers.Capability{Reason: reason}
}

func (provider *Provider) request(ctx context.Context, method, relativePath string, body any, target any) error {
	requestURL := *provider.apiBase
	requestURL.Path = path.Join(provider.apiBase.Path, strings.SplitN(relativePath, "?", 2)[0])
	if strings.Contains(relativePath, "?") {
		requestURL.RawQuery = strings.SplitN(relativePath, "?", 2)[1]
	}
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return &providers.Error{Code: providers.ErrorInvalidConfig, Message: "VirtFusion request is invalid"}
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, requestURL.String(), reader)
	if err != nil {
		return &providers.Error{Code: providers.ErrorInvalidConfig, Message: "VirtFusion request is invalid"}
	}
	request.Header.Set("Authorization", "Bearer "+provider.token)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := provider.client.Do(request)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return &providers.Error{Code: providers.ErrorNetwork, Message: "VirtFusion request timed out", Retryable: true}
		}
		return &providers.Error{Code: providers.ErrorNetwork, Message: "VirtFusion network request failed", Retryable: true}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))
		return classifyStatus(response.StatusCode)
	}
	if target == nil || response.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))
		return nil
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes+1))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return &providers.Error{Code: providers.ErrorProvider, Message: "VirtFusion returned malformed JSON"}
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return &providers.Error{Code: providers.ErrorProvider, Message: "VirtFusion returned an oversized or invalid response"}
	}
	return nil
}

func classifyStatus(status int) error {
	switch status {
	case http.StatusUnauthorized:
		return &providers.Error{Code: providers.ErrorAuthentication, Message: "VirtFusion credentials were rejected"}
	case http.StatusForbidden:
		return &providers.Error{Code: providers.ErrorPermission, Message: "VirtFusion permission was denied"}
	case http.StatusNotFound:
		return &providers.Error{Code: providers.ErrorNotFound, Message: "VirtFusion resource was not found"}
	case http.StatusConflict, http.StatusUnprocessableEntity:
		return &providers.Error{Code: providers.ErrorProvider, Message: "VirtFusion rejected the operation"}
	case http.StatusTooManyRequests:
		return &providers.Error{Code: providers.ErrorRateLimited, Message: "VirtFusion request was rate limited", Retryable: true}
	default:
		return &providers.Error{Code: providers.ErrorProvider, Message: "VirtFusion request failed", Retryable: status >= 500}
	}
}

func normalizeEndpoint(raw string, allowHTTP bool) (*url.URL, *url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, nil, errors.New("VirtFusion endpoint must be an absolute control-panel URL")
	}
	if parsed.Scheme != "https" && !(allowHTTP && parsed.Scheme == "http") {
		return nil, nil, errors.New("VirtFusion endpoint must use HTTPS")
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	if strings.HasSuffix(parsed.Path, "/api/v1") {
		parsed.Path = strings.TrimSuffix(parsed.Path, "/api/v1")
	}
	portal := *parsed
	api := *parsed
	api.Path = path.Join(parsed.Path, "/api/v1")
	return &portal, &api, nil
}

func normalizeServerID(raw string) (string, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || id < 1 {
		return "", errors.New("invalid server ID")
	}
	return strconv.FormatInt(id, 10), nil
}

func decodeCursor(cursor *providers.Cursor) (int, error) {
	if cursor == nil {
		return 1, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor.Value)
	if err != nil {
		return 0, err
	}
	var value pageCursor
	if json.Unmarshal(raw, &value) != nil || value.Page < 1 {
		return 0, errors.New("invalid cursor")
	}
	return value.Page, nil
}

func encodeCursor(page int) string {
	raw, _ := json.Marshal(pageCursor{Page: page})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeStrict(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("JSON must contain one value")
	}
	return nil
}

type endpointPolicy struct {
	resolver            Resolver
	allowedPrivateCIDRs []*net.IPNet
	allowLoopback       bool
}

func (policy endpointPolicy) validate(ctx context.Context, target *url.URL) error {
	if target == nil || target.Hostname() == "" {
		return errors.New("missing endpoint host")
	}
	addresses, err := policy.resolve(ctx, target.Hostname())
	if err != nil || len(addresses) == 0 {
		return errors.New("resolve endpoint")
	}
	for _, address := range addresses {
		if !policy.addressAllowed(address) {
			return errors.New("endpoint address is not allowed")
		}
	}
	return nil
}

func (policy endpointPolicy) resolve(ctx context.Context, host string) ([]net.IP, error) {
	if literal := net.ParseIP(host); literal != nil {
		return []net.IP{literal}, nil
	}
	return policy.resolver.LookupIP(ctx, "ip", host)
}

func (policy endpointPolicy) addressAllowed(address net.IP) bool {
	if address == nil || address.IsUnspecified() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.IsMulticast() {
		return false
	}
	if address.IsLoopback() {
		return policy.allowLoopback
	}
	if !address.IsPrivate() {
		return true
	}
	for _, network := range policy.allowedPrivateCIDRs {
		if network != nil && network.Contains(address) {
			return true
		}
	}
	return false
}

func newHTTPClient(policy endpointPolicy, origin *url.URL, allowHTTP bool) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, errors.New("invalid VirtFusion network address")
			}
			addresses, err := policy.resolve(ctx, host)
			if err != nil || len(addresses) == 0 {
				return nil, errors.New("resolve VirtFusion endpoint")
			}
			for _, resolved := range addresses {
				if !policy.addressAllowed(resolved) {
					return nil, errors.New("VirtFusion endpoint address changed to a blocked network")
				}
			}
			var lastErr error
			for _, resolved := range addresses {
				connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(resolved.String(), port))
				if dialErr == nil {
					return connection, nil
				}
				lastErr = dialErr
			}
			return nil, lastErr
		},
		ForceAttemptHTTP2:   true,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		MaxIdleConns:        20,
		IdleConnTimeout:     60 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
		CheckRedirect: func(request *http.Request, _ []*http.Request) error {
			if request.URL.Scheme != "https" && !(allowHTTP && request.URL.Scheme == "http") {
				return errors.New("VirtFusion redirect changed protocol")
			}
			if !sameHost(request.URL, origin) {
				return errors.New("VirtFusion cross-origin redirect rejected")
			}
			return policy.validate(request.Context(), request.URL)
		},
	}
}

func sameHost(left, right *url.URL) bool {
	return strings.EqualFold(left.Hostname(), right.Hostname()) && effectivePort(left) == effectivePort(right)
}

func effectivePort(target *url.URL) string {
	if target.Port() != "" {
		return target.Port()
	}
	switch target.Scheme {
	case "https", "wss":
		return "443"
	case "http", "ws":
		return "80"
	default:
		return ""
	}
}

var _ providers.Factory = (*Factory)(nil)
var _ providers.Provider = (*Provider)(nil)
