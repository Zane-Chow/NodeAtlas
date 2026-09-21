package virtualizor

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"controlpanel/internal/providers"
	providernetwork "controlpanel/internal/providers/network"
)

const maxResponseBytes = 4 << 20

type FactoryOptions struct{ AllowedPrivateCIDRs []*net.IPNet }
type factoryOptions struct {
	allowedPrivateCIDRs []*net.IPNet
	resolver            providernetwork.Resolver
	allowLoopback       bool
	rootCAs             *x509.CertPool
}
type Factory struct{ options factoryOptions }
type credentials struct {
	APIKey      string `json:"api_key"`
	APIPassword string `json:"api_password"`
}
type Provider struct {
	panelURL    *url.URL
	apiURL      *url.URL
	apiKey      string
	apiPassword string
	client      *http.Client
	policy      providernetwork.Policy
}

func NewFactory(options FactoryOptions) *Factory {
	return newFactory(factoryOptions{allowedPrivateCIDRs: options.AllowedPrivateCIDRs})
}
func newFactory(options factoryOptions) *Factory { return &Factory{options: options} }

func (factory *Factory) Create(config providers.ConnectionConfig) (providers.Provider, error) {
	if factory == nil || strings.TrimSpace(config.ID) == "" {
		return nil, invalidConfig("Virtualizor connection ID is required")
	}
	var secret credentials
	decoder := json.NewDecoder(bytes.NewReader(config.Credentials))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&secret) != nil || decoder.Decode(&struct{}{}) != io.EOF || strings.TrimSpace(secret.APIKey) == "" || strings.TrimSpace(secret.APIPassword) == "" {
		return nil, invalidConfig("invalid Virtualizor credentials")
	}
	panel, err := url.Parse(strings.TrimSpace(config.Endpoint))
	if err != nil || panel.Scheme != "https" || panel.Hostname() == "" || panel.User != nil || panel.Opaque != "" || panel.RawQuery != "" || panel.ForceQuery || strings.Contains(config.Endpoint, "#") || (panel.Path != "" && panel.Path != "/") || panel.RawPath != "" {
		return nil, invalidConfig("Virtualizor endpoint must be an absolute HTTPS origin without userinfo, query, or fragment")
	}
	if port := panel.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return nil, invalidConfig("invalid Virtualizor endpoint port")
		}
	}
	panel.Path = "/index.php"
	api := *panel
	policy := providernetwork.NewPolicy(factory.options.resolver, providernetwork.PolicyOptions{AllowedPrivateCIDRs: factory.options.allowedPrivateCIDRs, AllowLoopback: factory.options.allowLoopback})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if policy.Validate(ctx, panel) != nil {
		return nil, invalidConfig("Virtualizor endpoint is not allowed")
	}
	client := providernetwork.NewHTTPClient(policy, panel, providernetwork.HTTPOptions{RootCAs: factory.options.rootCAs})
	// Keep redirect failures bounded and reject userinfo as well as origin changes.
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 10 || request.URL.User != nil || !providernetwork.SameOrigin(panel, request.URL) {
			return errors.New("Virtualizor redirect rejected")
		}
		return policy.Validate(request.Context(), request.URL)
	}
	return &Provider{panelURL: panel, apiURL: &api, apiKey: secret.APIKey, apiPassword: secret.APIPassword, client: client, policy: policy}, nil
}

type flexString string

func (value *flexString) UnmarshalJSON(raw []byte) error {
	var s string
	if json.Unmarshal(raw, &s) == nil && string(raw) != "null" {
		*value = flexString(s)
		return nil
	}
	if string(raw) == "true" || string(raw) == "false" {
		*value = flexString(raw)
		return nil
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil && string(raw) != "null" {
		*value = flexString(n.String())
		return nil
	}
	return errors.New("invalid scalar")
}

type flexInt64 int64

func (value *flexInt64) UnmarshalJSON(raw []byte) error {
	var scalar flexString
	if err := json.Unmarshal(raw, &scalar); err != nil {
		return err
	}
	s := string(scalar)
	if s == "true" {
		s = "1"
	}
	if s == "false" {
		s = "0"
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return errors.New("invalid integer")
	}
	*value = flexInt64(n)
	return nil
}

type flexBool bool

func (value *flexBool) UnmarshalJSON(raw []byte) error {
	var scalar flexString
	if err := json.Unmarshal(raw, &scalar); err != nil {
		return err
	}
	switch string(scalar) {
	case "1", "true":
		*value = true
	case "0", "false":
		*value = false
	default:
		return errors.New("invalid boolean")
	}
	return nil
}

type serverData struct {
	ID         flexInt64   `json:"vpsid"`
	Hostname   flexString  `json:"hostname"`
	Virt       flexString  `json:"virt"`
	Status     flexInt64   `json:"status"`
	Suspended  flexBool    `json:"suspended"`
	VNC        flexBool    `json:"vnc"`
	Cores      flexInt64   `json:"cores"`
	RAM        flexInt64   `json:"ram"`
	Space      flexInt64   `json:"space"`
	Bandwidth  flexInt64   `json:"bandwidth"`
	ServerName flexString  `json:"server_name"`
	ServerID   flexString  `json:"serid"`
	IPs        ipAddresses `json:"ips"`
}

type ipAddresses []string

func (addresses *ipAddresses) UnmarshalJSON(raw []byte) error {
	if string(raw) == "null" {
		return errors.New("invalid IP addresses")
	}
	var single string
	if json.Unmarshal(raw, &single) == nil {
		*addresses = []string{single}
		return nil
	}
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		*addresses = list
		return nil
	}
	var entries map[string]string
	if json.Unmarshal(raw, &entries) != nil {
		return errors.New("invalid IP addresses")
	}
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	*addresses = make([]string, 0, len(keys))
	for _, key := range keys {
		*addresses = append(*addresses, entries[key])
	}
	return nil
}

func (provider *Provider) ValidateConnection(ctx context.Context) (providers.ConnectionInfo, error) {
	if _, err := provider.ListServers(ctx, nil); err != nil {
		return providers.ConnectionInfo{}, err
	}
	return providers.ConnectionInfo{DisplayName: "Virtualizor", Version: "Enduser API"}, nil
}

func (provider *Provider) ListServers(ctx context.Context, cursor *providers.Cursor) (providers.ServerPage, error) {
	if cursor != nil {
		return providers.ServerPage{}, invalidConfig("Virtualizor inventory does not support cursors")
	}
	var raw map[string]json.RawMessage
	if err := provider.request(ctx, url.Values{"act": {"listvs"}}, &raw); err != nil {
		return providers.ServerPage{}, err
	}
	if raw == nil {
		return providers.ServerPage{}, providerFailure("Virtualizor returned invalid inventory")
	}
	type entry struct {
		id  int64
		key string
	}
	entries := make([]entry, 0)
	for key := range raw {
		if key == "" || strings.IndexFunc(key, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
			continue
		}
		id, err := strconv.ParseInt(key, 10, 64)
		if err != nil || id < 1 || strconv.FormatInt(id, 10) != key {
			return providers.ServerPage{}, providerFailure("Virtualizor returned an invalid VPS ID")
		}
		entries = append(entries, entry{id: id, key: key})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].id < entries[j].id })
	page := providers.ServerPage{Servers: make([]providers.RemoteServer, 0, len(entries))}
	for _, entry := range entries {
		var data serverData
		if json.Unmarshal(raw[entry.key], &data) != nil || int64(data.ID) != entry.id {
			return providers.ServerPage{}, providerFailure("Virtualizor returned malformed or mismatched VPS data")
		}
		server, err := normalizeServer(data)
		if err != nil {
			return providers.ServerPage{}, err
		}
		page.Servers = append(page.Servers, server)
	}
	return page, nil
}

func (provider *Provider) GetServer(ctx context.Context, ref providers.ServerRef) (providers.RemoteServer, error) {
	id, err := normalizeServerID(ref.ExternalID)
	if err != nil {
		return providers.RemoteServer{}, err
	}
	var response struct {
		Info struct {
			VPS        serverData  `json:"vps"`
			Status     *flexInt64  `json:"status"`
			IP         ipAddresses `json:"ip"`
			ServerName flexString  `json:"server_name"`
		} `json:"info"`
	}
	if err := provider.request(ctx, url.Values{"act": {"vpsmanage"}, "svs": {id}}, &response); err != nil {
		return providers.RemoteServer{}, err
	}
	data := response.Info.VPS
	if strconv.FormatInt(int64(data.ID), 10) != id {
		return providers.RemoteServer{}, providerFailure("Virtualizor returned mismatched VPS data")
	}
	if response.Info.Status == nil {
		return providers.RemoteServer{}, providerFailure("Virtualizor returned missing VPS status")
	}
	data.Status = *response.Info.Status
	data.IPs = response.Info.IP
	if response.Info.ServerName != "" {
		data.ServerName = response.Info.ServerName
	}
	return normalizeServer(data)
}

func normalizeServer(data serverData) (providers.RemoteServer, error) {
	if data.Cores < 0 || data.RAM < 0 || data.Space < 0 || data.Bandwidth < 0 {
		return providers.RemoteServer{}, providerFailure("Virtualizor returned invalid VPS resources")
	}
	id := strconv.FormatInt(int64(data.ID), 10)
	scope := string(data.ServerName)
	if scope == "" {
		scope = string(data.ServerID)
	}
	name := string(data.Hostname)
	if strings.TrimSpace(name) == "" {
		name = "Virtualizor server " + id
	}
	spec, _ := json.Marshal(map[string]any{"virt": data.Virt, "cpu": data.Cores, "memory_mb": data.RAM, "storage_gb": data.Space, "bandwidth_gb": data.Bandwidth})
	addresses := make([]map[string]string, 0, len(data.IPs))
	for _, raw := range data.IPs {
		ip := net.ParseIP(strings.TrimSpace(raw))
		if ip == nil {
			return providers.RemoteServer{}, providerFailure("Virtualizor returned an invalid IP address")
		}
		kind := "ipv6"
		if ip.To4() != nil {
			kind = "ipv4"
		}
		addresses = append(addresses, map[string]string{"type": kind, "address": ip.String()})
	}
	encodedAddresses, _ := json.Marshal(addresses)
	state := mapState(int64(data.Status), bool(data.Suspended))
	// enable_console controls the OpenVZ serial console, not KVM VNC.
	console := bool(data.VNC) && state != providers.StateSuspended
	return providers.RemoteServer{ExternalID: id, Scope: scope, Name: name, State: state, RemoteState: strconv.FormatInt(int64(data.Status), 10), Spec: spec, Addresses: encodedAddresses, Capabilities: providers.Capabilities{
		CanStart:             capability(state == providers.StateStopped, "server must be stopped"),
		CanStop:              capability(state == providers.StateRunning, "server must be running"),
		CanReboot:            capability(state == providers.StateRunning, "server must be running"),
		CanEmbedConsole:      capability(console, "Virtualizor VNC is unavailable for this server"),
		CanOpenConsoleWindow: capability(console, "Virtualizor VNC is unavailable for this server"),
		HasProviderPortal:    providers.Capability{Available: true},
	}}, nil
}
func mapState(status int64, suspended bool) providers.ServerState {
	if suspended || status == 2 {
		return providers.StateSuspended
	}
	switch status {
	case 1:
		return providers.StateRunning
	case 0:
		return providers.StateStopped
	default:
		return providers.StateUnknown
	}
}
func capability(available bool, reason string) providers.Capability {
	if available {
		return providers.Capability{Available: true}
	}
	return providers.Capability{Reason: reason}
}
func normalizeServerID(raw string) (string, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 1 || strconv.FormatInt(id, 10) != raw {
		return "", &providers.Error{Code: providers.ErrorNotFound, Message: "Virtualizor server was not found"}
	}
	return raw, nil
}

type actionResponse struct {
	Done struct {
		Message string `json:"msg"`
	} `json:"done"`
	Error json.RawMessage `json:"error"`
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
	var response actionResponse
	if err := provider.request(ctx, url.Values{"act": {action}, "svs": {id}, "do": {"1"}}, &response); err != nil {
		return providers.ActionReceipt{}, err
	}
	if len(response.Error) != 0 && string(response.Error) != "null" {
		return providers.ActionReceipt{}, providerFailure("Virtualizor rejected the operation")
	}
	if strings.TrimSpace(response.Done.Message) == "" {
		return providers.ActionReceipt{}, providerFailure("Virtualizor did not confirm the operation")
	}
	return providers.ActionReceipt{}, nil
}

func (provider *Provider) OpenConsole(ctx context.Context, ref providers.ServerRef, mode providers.ConsoleMode) (providers.ConsoleTarget, error) {
	if mode != providers.ConsoleEmbedded && mode != providers.ConsoleWindow {
		return providers.ConsoleTarget{}, &providers.Error{Code: providers.ErrorUnsupported, Message: "Virtualizor console mode is unsupported"}
	}
	server, err := provider.GetServer(ctx, ref)
	if err != nil {
		return providers.ConsoleTarget{}, err
	}
	if !server.Capabilities.CanEmbedConsole.Available {
		return providers.ConsoleTarget{}, &providers.Error{Code: providers.ErrorUnsupported, Message: "Virtualizor VNC is unavailable for this server"}
	}
	var response struct {
		IP       string    `json:"ip"`
		Port     flexInt64 `json:"port"`
		Password string    `json:"password"`
		NoVNC    flexBool  `json:"novnc"`
	}
	id := server.ExternalID
	if err := provider.request(ctx, url.Values{"act": {"vnc"}, "svs": {id}, "novnc": {id}, "do": {"add"}}, &response); err != nil {
		return providers.ConsoleTarget{}, err
	}
	if response.Port < 1 || response.Port > 65535 {
		return providers.ConsoleTarget{}, providerFailure("Virtualizor returned an invalid VNC port")
	}
	ip := net.ParseIP(response.IP)
	if ip == nil {
		return providers.ConsoleTarget{}, providerFailure("Virtualizor returned an invalid VNC address")
	}
	target := &url.URL{Scheme: "vnc+tcp", Host: net.JoinHostPort(ip.String(), strconv.FormatInt(int64(response.Port), 10))}
	if provider.policy.Validate(ctx, target) != nil {
		return providers.ConsoleTarget{}, providerFailure("Virtualizor returned an untrusted VNC address")
	}
	if !response.NoVNC {
		return providers.ConsoleTarget{}, &providers.Error{Code: providers.ErrorUnsupported, Message: "Virtualizor VNC is unavailable for this server"}
	}
	return providers.ConsoleTarget{Mode: mode, Protocol: "rfb", URL: target, Password: response.Password}, nil
}

func (provider *Provider) ProviderPortalURL(_ context.Context, ref providers.ServerRef) (*url.URL, error) {
	id, err := normalizeServerID(ref.ExternalID)
	if err != nil {
		return nil, err
	}
	portal := *provider.panelURL
	portal.RawQuery = url.Values{"act": {"vpsmanage"}, "svs": {id}}.Encode()
	return &portal, nil
}

func (provider *Provider) request(ctx context.Context, query url.Values, target any) error {
	requestURL := *provider.apiURL
	query.Set("api", "json")
	query.Set("apikey", provider.apiKey)
	query.Set("apipass", provider.apiPassword)
	requestURL.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return invalidConfig("invalid Virtualizor request")
	}
	request.Header.Set("Accept", "application/json")
	response, err := provider.client.Do(request)
	if err != nil {
		message := "Virtualizor network request failed"
		if errors.Is(err, context.DeadlineExceeded) {
			message = "Virtualizor request timed out"
		}
		return &providers.Error{Code: providers.ErrorNetwork, Message: message, Retryable: true}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))
		return classifyStatus(response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return &providers.Error{Code: providers.ErrorNetwork, Message: "Virtualizor response could not be read", Retryable: true}
	}
	if len(raw) > maxResponseBytes {
		return providerFailure("Virtualizor returned an oversized response")
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(raw, &envelope) != nil || envelope == nil {
		return providerFailure("Virtualizor returned malformed JSON")
	}
	if apiError := envelope["error"]; len(apiError) != 0 && string(apiError) != "null" {
		return providerFailure("Virtualizor rejected the operation")
	}
	if json.Unmarshal(raw, target) != nil {
		return providerFailure("Virtualizor returned malformed JSON")
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
		return &providers.Error{Code: providers.ErrorAuthentication, Message: "Virtualizor credentials were rejected"}
	case http.StatusForbidden:
		return &providers.Error{Code: providers.ErrorPermission, Message: "Virtualizor permission was denied"}
	case http.StatusNotFound:
		return &providers.Error{Code: providers.ErrorNotFound, Message: "Virtualizor resource was not found"}
	case http.StatusTooManyRequests:
		return &providers.Error{Code: providers.ErrorRateLimited, Message: "Virtualizor request was rate limited", Retryable: true}
	default:
		return &providers.Error{Code: providers.ErrorProvider, Message: "Virtualizor request failed", Retryable: status >= 500}
	}
}

var _ providers.Factory = (*Factory)(nil)
var _ providers.Provider = (*Provider)(nil)
