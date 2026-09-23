// Package solusvm2 implements the SolusVM 2 management API v1.
package solusvm2

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"controlpanel/internal/providers"
	providernetwork "controlpanel/internal/providers/network"
	"github.com/google/uuid"
)

const (
	maxResponseBytes = 4 << 20
	maxPage          = 1_000_000
)

type FactoryOptions struct{ AllowedPrivateCIDRs []*net.IPNet }
type factoryOptions struct {
	allowedPrivateCIDRs []*net.IPNet
	resolver            providernetwork.Resolver
	allowLoopback       bool
	rootCAs             *x509.CertPool
}
type Factory struct{ options factoryOptions }
type Provider struct {
	apiBase  *url.URL
	panelURL *url.URL
	token    string
	client   *http.Client
	policy   providernetwork.Policy
}
type credentials struct {
	APIToken string `json:"api_token"`
}

func NewFactory(options FactoryOptions) *Factory {
	return newFactory(factoryOptions{allowedPrivateCIDRs: options.AllowedPrivateCIDRs})
}
func newFactory(options factoryOptions) *Factory { return &Factory{options: options} }

func (factory *Factory) Create(config providers.ConnectionConfig) (providers.Provider, error) {
	if factory == nil || strings.TrimSpace(config.ID) == "" {
		return nil, invalidConfig("SolusVM 2 connection ID is required")
	}
	settings := config.Settings
	if len(settings) == 0 {
		settings = json.RawMessage(`{}`)
	}
	if decodeStrictObject(settings, &struct{}{}) != nil {
		return nil, invalidConfig("invalid SolusVM 2 settings")
	}
	var secret credentials
	if decodeStrictObject(config.Credentials, &secret) != nil || !validToken(secret.APIToken) {
		return nil, invalidConfig("invalid SolusVM 2 credentials")
	}
	panel, api, err := normalizeEndpoint(config.Endpoint)
	if err != nil {
		return nil, err
	}
	policy := providernetwork.NewPolicy(factory.options.resolver, providernetwork.PolicyOptions{AllowedPrivateCIDRs: factory.options.allowedPrivateCIDRs, AllowLoopback: factory.options.allowLoopback})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if policy.Validate(ctx, panel) != nil {
		return nil, invalidConfig("SolusVM 2 endpoint is not allowed")
	}
	client := providernetwork.NewHTTPClient(policy, panel, providernetwork.HTTPOptions{RootCAs: factory.options.rootCAs, Timeout: 30 * time.Second})
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 10 || request.URL.User != nil || request.URL.Fragment != "" || request.URL.Scheme != "https" || !providernetwork.SameOrigin(panel, request.URL) {
			return errors.New("SolusVM 2 redirect rejected")
		}
		return policy.Validate(request.Context(), request.URL)
	}
	return &Provider{apiBase: api, panelURL: panel, token: secret.APIToken, client: client, policy: policy}, nil
}

func normalizeEndpoint(raw string) (*url.URL, *url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.Opaque != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || strings.Contains(raw, "#") || parsed.RawPath != "" || strings.Contains(parsed.Hostname(), "%") || strings.HasSuffix(parsed.Host, ":") {
		return nil, nil, invalidConfig("SolusVM 2 endpoint must be an absolute HTTPS panel origin or API v1 root")
	}
	if parsed.Path != "" && parsed.Path != "/" && parsed.Path != "/api/v1" && parsed.Path != "/api/v1/" {
		return nil, nil, invalidConfig("invalid SolusVM 2 endpoint path")
	}
	if port := parsed.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
			return nil, nil, invalidConfig("invalid SolusVM 2 endpoint port")
		}
	}
	panel, api := *parsed, *parsed
	panel.Path = "/"
	api.Path = "/api/v1"
	return &panel, &api, nil
}

func validToken(token string) bool {
	if len(token) == 0 || len(token) > 8192 {
		return false
	}
	for _, c := range token {
		if c <= 32 || c >= 127 {
			return false
		}
	}
	return true
}

func decodeStrictObject(raw []byte, target any) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return errors.New("object is required")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("one JSON object is required")
	}
	return nil
}

// DTOs intentionally omit user objects, VNC URLs, and all settings secrets.
type serverData struct {
	ID                 json.Number `json:"id"`
	Name               string      `json:"name"`
	Status             string      `json:"status"`
	RealStatus         string      `json:"real_status"`
	Suspended          bool        `json:"is_suspended"`
	Processing         bool        `json:"is_processing"`
	VirtualizationType string      `json:"virtualization_type"`
	OSType             string      `json:"os_type"`
	Project            struct {
		ID json.Number `json:"id"`
	} `json:"project"`
	Specifications struct {
		VCPU int64 `json:"vcpu"`
		RAM  int64 `json:"ram"`
		Disk int64 `json:"disk"`
	} `json:"specifications"`
	Settings struct {
		VNCEnabled bool `json:"vnc_enabled"`
	} `json:"settings"`
	IPs []struct {
		IP string `json:"ip"`
	} `json:"ips"`
	IPAddresses *struct {
		IPv4 []struct {
			IP string `json:"ip"`
		} `json:"ipv4"`
		IPv6 []struct {
			PrimaryIP string `json:"primary_ip"`
		} `json:"ipv6"`
	} `json:"ip_addresses"`
}
type detailResponse struct {
	Data serverData `json:"data"`
}
type listResponse struct {
	Data []serverData `json:"data"`
	Meta struct {
		CurrentPage int `json:"current_page"`
		LastPage    int `json:"last_page"`
	} `json:"meta"`
}

func (provider *Provider) ValidateConnection(ctx context.Context) (providers.ConnectionInfo, error) {
	if _, err := provider.ListServers(ctx, nil); err != nil {
		return providers.ConnectionInfo{}, err
	}
	return providers.ConnectionInfo{DisplayName: "SolusVM 2", Version: "API v1"}, nil
}

func (provider *Provider) ListServers(ctx context.Context, cursor *providers.Cursor) (providers.ServerPage, error) {
	page := int64(1)
	if cursor != nil {
		var err error
		page, err = positiveInteger(cursor.Value)
		if err != nil || page > maxPage {
			return providers.ServerPage{}, invalidConfig("invalid SolusVM 2 inventory cursor")
		}
	}
	var response listResponse
	if err := provider.request(ctx, http.MethodGet, "/servers", url.Values{"page": {strconv.FormatInt(page, 10)}}, nil, &response); err != nil {
		return providers.ServerPage{}, err
	}
	if response.Data == nil || response.Meta.CurrentPage != int(page) || response.Meta.LastPage < int(page) || response.Meta.LastPage > maxPage || (int(page) < response.Meta.LastPage && len(response.Data) == 0) {
		return providers.ServerPage{}, providerFailure("SolusVM 2 returned invalid pagination data")
	}
	result := providers.ServerPage{Servers: make([]providers.RemoteServer, 0, len(response.Data))}
	seen := make(map[string]struct{}, len(response.Data))
	for _, data := range response.Data {
		server, err := normalizeServer(data)
		if err != nil {
			return providers.ServerPage{}, err
		}
		if _, exists := seen[server.ExternalID]; exists {
			return providers.ServerPage{}, providerFailure("SolusVM 2 returned duplicate server IDs")
		}
		seen[server.ExternalID] = struct{}{}
		result.Servers = append(result.Servers, server)
	}
	if int(page) < response.Meta.LastPage {
		result.Next = &providers.Cursor{Value: strconv.FormatInt(page+1, 10)}
	}
	return result, nil
}

func (provider *Provider) GetServer(ctx context.Context, ref providers.ServerRef) (providers.RemoteServer, error) {
	id, err := normalizeServerID(ref.ExternalID)
	if err != nil {
		return providers.RemoteServer{}, err
	}
	var response detailResponse
	if err := provider.request(ctx, http.MethodGet, "/servers/"+id, nil, nil, &response); err != nil {
		return providers.RemoteServer{}, err
	}
	if response.Data.ID.String() != id {
		return providers.RemoteServer{}, providerFailure("SolusVM 2 returned mismatched server data")
	}
	return normalizeServer(response.Data)
}

func normalizeServer(data serverData) (providers.RemoteServer, error) {
	id := data.ID.String()
	if _, err := positiveInteger(id); err != nil {
		return providers.RemoteServer{}, providerFailure("SolusVM 2 returned an invalid server ID")
	}
	scope := "default"
	if data.Project.ID != "" {
		if _, err := positiveInteger(data.Project.ID.String()); err != nil {
			return providers.RemoteServer{}, providerFailure("SolusVM 2 returned an invalid project ID")
		}
		scope = data.Project.ID.String()
	}
	if data.Specifications.VCPU < 0 || data.Specifications.RAM < 0 || data.Specifications.Disk < 0 {
		return providers.RemoteServer{}, providerFailure("SolusVM 2 returned invalid server resources")
	}
	name := strings.TrimSpace(data.Name)
	if name == "" {
		name = "SolusVM 2 server " + id
	}
	virt, osType := data.VirtualizationType, data.OSType
	if virt != "kvm" && virt != "vz" {
		virt = "unknown"
	}
	if osType != "Linux" && osType != "Windows" {
		osType = "unknown"
	}
	spec, _ := json.Marshal(map[string]any{"virt": virt, "os_type": osType, "cpu": data.Specifications.VCPU, "memory_mb": data.Specifications.RAM / (1 << 20), "storage_gb": data.Specifications.Disk})
	addresses := make([]map[string]string, 0)
	seen := make(map[string]bool)
	addIP := func(raw, expected string) error {
		ip := net.ParseIP(strings.TrimSpace(raw))
		if ip == nil {
			return providerFailure("SolusVM 2 returned an invalid IP address")
		}
		kind := "ipv6"
		if ip.To4() != nil {
			kind = "ipv4"
		}
		if expected != "" && expected != kind {
			return providerFailure("SolusVM 2 returned an invalid IP address family")
		}
		if !seen[ip.String()] {
			addresses = append(addresses, map[string]string{"type": kind, "address": ip.String()})
			seen[ip.String()] = true
		}
		return nil
	}
	if data.IPAddresses != nil {
		for _, ip := range data.IPAddresses.IPv4 {
			if err := addIP(ip.IP, "ipv4"); err != nil {
				return providers.RemoteServer{}, err
			}
		}
		for _, ip := range data.IPAddresses.IPv6 {
			if err := addIP(ip.PrimaryIP, "ipv6"); err != nil {
				return providers.RemoteServer{}, err
			}
		}
	} else {
		for _, ip := range data.IPs {
			if err := addIP(ip.IP, ""); err != nil {
				return providers.RemoteServer{}, err
			}
		}
	}
	encodedAddresses, _ := json.Marshal(addresses)
	state, remote := mapState(data)
	console := data.Settings.VNCEnabled && !data.Suspended
	return providers.RemoteServer{ExternalID: id, Scope: scope, Name: name, State: state, RemoteState: remote, Spec: spec, Addresses: encodedAddresses, Capabilities: providers.Capabilities{
		CanStart: capability(state == providers.StateStopped, "server must be stopped"), CanStop: capability(state == providers.StateRunning, "server must be running"), CanReboot: capability(state == providers.StateRunning, "server must be running"),
		CanEmbedConsole: capability(console, "SolusVM 2 VNC is unavailable for this server"), CanOpenConsoleWindow: capability(console, "SolusVM 2 VNC is unavailable for this server"), HasProviderPortal: providers.Capability{Available: true},
	}}, nil
}

func mapState(data serverData) (providers.ServerState, string) {
	if data.Suspended {
		return providers.StateSuspended, "suspended"
	}
	if data.Processing || data.Status == "processing" || data.RealStatus == "processing" {
		return providers.StatePending, "processing"
	}
	remote := data.RealStatus
	if remote == "" {
		remote = data.Status
	}
	switch remote {
	case "started":
		return providers.StateRunning, remote
	case "stopped":
		return providers.StateStopped, remote
	case "not exist":
		return providers.StateError, remote
	case "unavailable":
		return providers.StateUnknown, remote
	default:
		return providers.StateUnknown, "unknown"
	}
}
func capability(available bool, reason string) providers.Capability {
	if available {
		return providers.Capability{Available: true}
	}
	return providers.Capability{Reason: reason}
}
func positiveInteger(raw string) (int64, error) {
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 1 || strconv.FormatInt(n, 10) != raw {
		return 0, errors.New("invalid positive integer")
	}
	return n, nil
}
func normalizeServerID(raw string) (string, error) {
	if _, err := positiveInteger(raw); err != nil {
		return "", &providers.Error{Code: providers.ErrorNotFound, Message: "SolusVM 2 server was not found"}
	}
	return raw, nil
}

func (provider *Provider) StartServer(ctx context.Context, ref providers.ServerRef) (providers.ActionReceipt, error) {
	return provider.power(ctx, ref, "start")
}
func (provider *Provider) StopServer(ctx context.Context, ref providers.ServerRef) (providers.ActionReceipt, error) {
	return provider.power(ctx, ref, "stop")
}
func (provider *Provider) RebootServer(ctx context.Context, ref providers.ServerRef) (providers.ActionReceipt, error) {
	return provider.power(ctx, ref, "restart")
}
func (provider *Provider) power(ctx context.Context, ref providers.ServerRef, action string) (providers.ActionReceipt, error) {
	id, err := normalizeServerID(ref.ExternalID)
	if err != nil {
		return providers.ActionReceipt{}, err
	}
	var body any
	if action != "start" {
		body = struct {
			Force bool `json:"force"`
		}{Force: false}
	}
	var response struct {
		Data struct {
			ID json.Number `json:"id"`
		} `json:"data"`
	}
	if err := provider.request(ctx, http.MethodPost, "/servers/"+id+"/"+action, nil, body, &response); err != nil {
		return providers.ActionReceipt{}, err
	}
	if _, err := positiveInteger(response.Data.ID.String()); err != nil {
		return providers.ActionReceipt{}, providerFailure("SolusVM 2 did not return a valid task ID")
	}
	return providers.ActionReceipt{RequestID: response.Data.ID.String()}, nil
}

func (provider *Provider) OpenConsole(ctx context.Context, ref providers.ServerRef, mode providers.ConsoleMode) (providers.ConsoleTarget, error) {
	if mode != providers.ConsoleEmbedded && mode != providers.ConsoleWindow {
		return providers.ConsoleTarget{}, &providers.Error{Code: providers.ErrorUnsupported, Message: "SolusVM 2 console mode is unsupported"}
	}
	server, err := provider.GetServer(ctx, ref)
	if err != nil {
		return providers.ConsoleTarget{}, err
	}
	if !server.Capabilities.CanEmbedConsole.Available {
		return providers.ConsoleTarget{}, &providers.Error{Code: providers.ErrorUnsupported, Message: "SolusVM 2 VNC is unavailable for this server"}
	}
	// vnc_up has a top-level transport envelope. Deliberately omit vnc_proxy_url:
	// only the configured panel origin is trusted to provide the WSS transport.
	var response struct {
		Host string `json:"host"`
		Port int    `json:"port"`
		VM   struct {
			ID       json.Number `json:"id"`
			UUID     string      `json:"uuid"`
			Settings struct {
				Password string `json:"vnc_password"`
			} `json:"settings"`
		} `json:"vm"`
	}
	if err := provider.request(ctx, http.MethodPost, "/servers/"+server.ExternalID+"/vnc_up", nil, nil, &response); err != nil {
		return providers.ConsoleTarget{}, err
	}
	id, err := uuid.Parse(response.VM.UUID)
	if err != nil || id == uuid.Nil || id.String() != response.VM.UUID || response.VM.ID.String() != server.ExternalID || response.Port < 1 || response.Port > 65535 || !validConsoleHost(response.Host) || !validConsolePassword(response.VM.Settings.Password) {
		return providers.ConsoleTarget{}, providerFailure("SolusVM 2 returned invalid VNC data")
	}
	addresses, err := provider.policy.ResolveAllowed(ctx, response.Host)
	if err != nil {
		return providers.ConsoleTarget{}, providerFailure("SolusVM 2 returned an untrusted VNC address")
	}
	// The management node opens the compute connection. Pin an allowed literal so
	// that it cannot resolve a supplied hostname again after our policy check.
	hosts := make([]string, len(addresses))
	for i, address := range addresses {
		hosts[i] = address.String()
	}
	sort.Strings(hosts)
	target := *provider.panelURL
	target.Scheme, target.Path = "wss", "/vnc"
	target.RawQuery = url.Values{"url": {net.JoinHostPort(hosts[0], strconv.Itoa(response.Port)) + "/" + response.VM.UUID}}.Encode()
	return providers.ConsoleTarget{Mode: mode, Protocol: "rfb", URL: &target, Password: response.VM.Settings.Password}, nil
}

func validConsoleHost(host string) bool {
	if net.ParseIP(host) != nil {
		return true
	}
	if host == "" || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

func validConsolePassword(password string) bool {
	if strings.TrimSpace(password) == "" || len(password) > 4096 {
		return false
	}
	for _, c := range password {
		if c < 32 || c == 127 {
			return false
		}
	}
	return true
}
func (provider *Provider) ProviderPortalURL(context.Context, providers.ServerRef) (*url.URL, error) {
	copy := *provider.panelURL
	return &copy, nil
}

func (provider *Provider) request(ctx context.Context, method, relativePath string, query url.Values, body, target any) error {
	requestURL := *provider.apiBase
	requestURL.Path += relativePath
	requestURL.RawQuery = query.Encode()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return invalidConfig("invalid SolusVM 2 request")
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, requestURL.String(), reader)
	if err != nil {
		return invalidConfig("invalid SolusVM 2 request")
	}
	request.Header.Set("Authorization", "Bearer "+provider.token)
	request.Header.Set("Accept", "application/json")
	if body != nil || method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := provider.client.Do(request)
	if err != nil {
		return &providers.Error{Code: providers.ErrorNetwork, Message: "SolusVM 2 network request failed", Retryable: true}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))
		return classifyStatus(response.StatusCode)
	}
	contentType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		return providerFailure("SolusVM 2 returned an invalid response content type")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return &providers.Error{Code: providers.ErrorNetwork, Message: "SolusVM 2 response could not be read", Retryable: true}
	}
	if len(raw) > maxResponseBytes {
		return providerFailure("SolusVM 2 returned an oversized response")
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' || json.Unmarshal(trimmed, target) != nil {
		return providerFailure("SolusVM 2 returned malformed JSON")
	}
	return nil
}

func invalidConfig(message string) error {
	return &providers.Error{Code: providers.ErrorInvalidConfig, Message: message}
}
func providerFailure(message string) error {
	return &providers.Error{Code: providers.ErrorProvider, Message: message}
}
func classifyStatus(status int) error {
	switch status {
	case http.StatusUnauthorized:
		return &providers.Error{Code: providers.ErrorAuthentication, Message: "SolusVM 2 credentials were rejected"}
	case http.StatusForbidden:
		return &providers.Error{Code: providers.ErrorPermission, Message: "SolusVM 2 permission was denied"}
	case http.StatusNotFound:
		return &providers.Error{Code: providers.ErrorNotFound, Message: "SolusVM 2 resource was not found"}
	case http.StatusConflict, http.StatusUnprocessableEntity:
		return providerFailure("SolusVM 2 rejected the operation for the current configuration or state")
	case http.StatusTooManyRequests:
		return &providers.Error{Code: providers.ErrorRateLimited, Message: "SolusVM 2 request was rate limited", Retryable: true}
	default:
		return &providers.Error{Code: providers.ErrorProvider, Message: "SolusVM 2 request failed", Retryable: status >= 500}
	}
}

var _ providers.Factory = (*Factory)(nil)
var _ providers.Provider = (*Provider)(nil)
