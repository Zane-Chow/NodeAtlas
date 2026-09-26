package virtfusion

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"

	"controlpanel/internal/providers"
	providernetwork "controlpanel/internal/providers/network"
)

const maxResponseBytes = 4 << 20

type FactoryOptions struct {
	AllowedPrivateCIDRs []*net.IPNet
}

type factoryOptions struct {
	allowedPrivateCIDRs []*net.IPNet
	resolver            providernetwork.Resolver
	allowHTTP           bool
	allowLoopback       bool
}

type Factory struct{ options factoryOptions }

type Provider struct {
	apiBase   *url.URL
	portalURL *url.URL
	token     string
	client    *http.Client
}

type settings struct {
	// PageSize is retained only so connections created by releases that used
	// the Global/Admin API can still be loaded and migrated. The User API does
	// not expose the old paginated /servers endpoint.
	PageSize int `json:"page_size,omitempty"`
}

type credentials struct {
	Token string `json:"token"`
}

type listResponse struct {
	Data json.RawMessage `json:"data"`
}

type detailResponse struct {
	Data serverData `json:"data"`
}

type serverData struct {
	UUID             string          `json:"uuid"`
	Name             string          `json:"name"`
	Hostname         string          `json:"hostname"`
	State            string          `json:"state"`
	Commissioned     *bool           `json:"commissioned"`
	CommissionStatus int             `json:"commissionStatus"`
	Suspended        bool            `json:"suspended"`
	Locked           bool            `json:"locked"`
	BuildFailed      bool            `json:"buildFailed"`
	BuildFailedAlt   bool            `json:"build_failed"`
	RemoteState      json.RawMessage `json:"remoteState"`
	RemoteStateAlt   json.RawMessage `json:"remote_state"`
	Memory           int             `json:"memory"`
	Storage          int             `json:"storage"`
	Traffic          int             `json:"traffic"`
	CPUCores         int             `json:"cpu_cores"`
	CPUCoresCamel    int             `json:"cpuCores"`
	Resources        struct {
		Memory        int `json:"memory"`
		Storage       int `json:"storage"`
		Traffic       int `json:"traffic"`
		CPUCores      int `json:"cpu_cores"`
		CPUCoresCamel int `json:"cpuCores"`
	} `json:"resources"`
	Settings struct {
		Resources struct {
			Memory   int `json:"memory"`
			Storage  int `json:"storage"`
			Traffic  int `json:"traffic"`
			CPUCores int `json:"cpuCores"`
		} `json:"resources"`
	} `json:"settings"`
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
		QueueID json.RawMessage `json:"queueId"`
		TaskID  json.RawMessage `json:"taskId"`
		ID      json.RawMessage `json:"id"`
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
		Password string `json:"password"`
		URL      string `json:"url"`
		WSS      struct {
			URL string `json:"url"`
		} `json:"wss"`
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
	if configuration.PageSize < 0 || configuration.PageSize > 200 {
		return nil, errors.New("VirtFusion legacy page_size must be between 0 and 200")
	}
	var secret credentials
	if err := decodeStrict(config.Credentials, &secret); err != nil || strings.TrimSpace(secret.Token) == "" {
		return nil, errors.New("invalid VirtFusion credentials")
	}
	portalURL, apiBase, err := normalizeEndpoint(config.Endpoint, factory.options.allowHTTP)
	if err != nil {
		return nil, err
	}
	policy := providernetwork.NewPolicy(factory.options.resolver, providernetwork.PolicyOptions{
		AllowedPrivateCIDRs: factory.options.allowedPrivateCIDRs,
		AllowLoopback:       factory.options.allowLoopback,
	})
	validationContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := policy.Validate(validationContext, portalURL); err != nil {
		return nil, errors.New("VirtFusion endpoint is not allowed")
	}
	client := providernetwork.NewHTTPClient(policy, portalURL, providernetwork.HTTPOptions{
		AllowHTTP: factory.options.allowHTTP,
	})
	return &Provider{apiBase: apiBase, portalURL: portalURL, token: strings.TrimSpace(secret.Token), client: client}, nil
}

func (provider *Provider) ValidateConnection(ctx context.Context) (providers.ConnectionInfo, error) {
	if err := provider.request(ctx, http.MethodGet, "/account", nil, nil); err != nil {
		return providers.ConnectionInfo{}, err
	}
	return providers.ConnectionInfo{DisplayName: "VirtFusion", Version: "User API"}, nil
}

func (provider *Provider) ListServers(ctx context.Context, cursor *providers.Cursor) (providers.ServerPage, error) {
	if cursor != nil {
		return providers.ServerPage{}, &providers.Error{Code: providers.ErrorInvalidConfig, Message: "invalid VirtFusion inventory cursor"}
	}
	var listed listResponse
	if err := provider.request(ctx, http.MethodGet, "/server", nil, &listed); err != nil {
		return providers.ServerPage{}, err
	}
	servers, err := decodeServerList(listed.Data)
	if err != nil {
		return providers.ServerPage{}, &providers.Error{Code: providers.ErrorProvider, Message: "VirtFusion returned invalid server inventory"}
	}
	result := providers.ServerPage{Servers: make([]providers.RemoteServer, 0, len(servers))}
	for _, item := range servers {
		id, err := normalizeServerID(item.UUID)
		if err != nil {
			return providers.ServerPage{}, &providers.Error{Code: providers.ErrorProvider, Message: "VirtFusion returned an invalid server ID"}
		}
		server, err := provider.getServer(ctx, id)
		if err != nil {
			return providers.ServerPage{}, err
		}
		result.Servers = append(result.Servers, server)
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
	if err := provider.request(ctx, http.MethodGet, "/server/"+id, nil, &response); err != nil {
		return providers.RemoteServer{}, err
	}
	responseID, err := normalizeServerID(response.Data.UUID)
	if err != nil || responseID != id {
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
	if err := provider.request(ctx, http.MethodPost, "/server/"+id+"/power/"+action, nil, &response); err != nil {
		return providers.ActionReceipt{}, err
	}
	return providers.ActionReceipt{RequestID: firstScalar(response.Data.QueueID, response.Data.TaskID, response.Data.ID)}, nil
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
	if err := provider.request(ctx, http.MethodGet, "/server/"+id+"/vnc", nil, &response); err != nil {
		return providers.ConsoleTarget{}, err
	}
	vncURL := firstNonEmpty(response.Data.VNC.WSS.URL, response.Data.WSS.URL, response.Data.URL)
	password := firstNonEmpty(response.Data.VNC.Password, response.Data.Password)
	target, err := provider.resolveVNC(vncURL)
	if err != nil {
		return providers.ConsoleTarget{}, err
	}
	return providers.ConsoleTarget{Mode: mode, Protocol: "rfb", URL: target, Password: password}, nil
}

func (provider *Provider) ProviderPortalURL(_ context.Context, ref providers.ServerRef) (*url.URL, error) {
	id, err := normalizeServerID(ref.ExternalID)
	if err != nil {
		return nil, &providers.Error{Code: providers.ErrorNotFound, Message: "VirtFusion server was not found"}
	}
	copy := *provider.portalURL
	copy.Path = path.Join(copy.Path, "server", id)
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
	vncOrigin := *provider.portalURL
	vncOrigin.Scheme = expectedScheme
	if target.Scheme != expectedScheme || !providernetwork.SameOrigin(target, &vncOrigin) || strings.TrimSpace(target.Path) == "" {
		return nil, &providers.Error{Code: providers.ErrorProvider, Message: "VirtFusion returned an untrusted VNC endpoint"}
	}
	return target, nil
}

func normalizeServer(data serverData) providers.RemoteServer {
	id := data.UUID
	name := strings.TrimSpace(data.Name)
	if name == "" {
		name = strings.TrimSpace(data.Hostname)
	}
	if name == "" {
		name = "VirtFusion server " + id
	}
	remoteState := decodeRemoteState(data.RemoteState)
	if remoteState == "unknown" {
		remoteState = decodeRemoteState(data.RemoteStateAlt)
	}
	if remoteState == "unknown" && !strings.EqualFold(strings.TrimSpace(data.State), "complete") {
		remoteState = strings.ToLower(strings.TrimSpace(data.State))
	}
	state := mapState(remoteState, data)
	memory := firstPositive(data.Memory, data.Resources.Memory, data.Settings.Resources.Memory)
	storage := firstPositive(data.Storage, data.Resources.Storage, data.Settings.Resources.Storage)
	traffic := firstPositive(data.Traffic, data.Resources.Traffic, data.Settings.Resources.Traffic)
	cpu := firstPositive(data.CPUCores, data.CPUCoresCamel, data.Resources.CPUCores, data.Resources.CPUCoresCamel, data.Settings.Resources.CPUCores)
	spec, _ := json.Marshal(map[string]int{
		"memory_mb":  memory,
		"storage_gb": storage,
		"traffic_gb": traffic,
		"cpu":        cpu,
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
	buildFailed := data.BuildFailed || data.BuildFailedAlt
	commissioned := data.Commissioned == nil || *data.Commissioned
	vncAvailable := commissioned && !data.Suspended && !data.Locked && !buildFailed
	capabilities := providers.Capabilities{
		CanStart:             capability(state == providers.StateStopped, "server must be stopped"),
		CanStop:              capability(state == providers.StateRunning, "server must be running"),
		CanReboot:            capability(state == providers.StateRunning, "server must be running"),
		CanEmbedConsole:      capability(vncAvailable, "VirtFusion VNC is unavailable for this server"),
		CanOpenConsoleWindow: capability(vncAvailable, "VirtFusion VNC is unavailable for this server"),
		HasProviderPortal:    providers.Capability{Available: true},
	}
	return providers.RemoteServer{ExternalID: id, Scope: "", Name: name, State: state, RemoteState: remoteState, Spec: spec, Addresses: encodedAddresses, Capabilities: capabilities}
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
	if data.BuildFailed || data.BuildFailedAlt {
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
	if data.Commissioned != nil && !*data.Commissioned {
		return providers.StatePending
	}
	if data.CommissionStatus > 0 && data.CommissionStatus < 3 {
		return providers.StatePending
	}
	if strings.TrimSpace(data.State) != "" && remote == "unknown" && !strings.EqualFold(data.State, "complete") {
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
	cleanPath := strings.TrimSuffix(parsed.Path, "/")
	if cleanPath == "/api/v1" {
		return nil, nil, errors.New("VirtFusion requires a User API endpoint, not /api/v1")
	}
	if cleanPath != "" && cleanPath != "/api" {
		return nil, nil, errors.New("VirtFusion endpoint must be the control-panel origin or end with /api")
	}
	parsed.Path = ""
	portal := *parsed
	api := *parsed
	api.Path = "/api"
	return &portal, &api, nil
}

func normalizeServerID(raw string) (string, error) {
	id, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", errors.New("invalid server ID")
	}
	return id.String(), nil
}

func decodeServerList(raw json.RawMessage) ([]serverData, error) {
	var direct []serverData
	if err := json.Unmarshal(raw, &direct); err == nil {
		return direct, nil
	}
	var wrapped struct {
		Servers []serverData `json:"servers"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil || wrapped.Servers == nil {
		return nil, errors.New("invalid server list")
	}
	return wrapped.Servers, nil
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstScalar(values ...json.RawMessage) string {
	for _, raw := range values {
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}
		var value string
		if json.Unmarshal(raw, &value) == nil {
			return strings.TrimSpace(value)
		}
		var number json.Number
		if json.Unmarshal(raw, &number) == nil {
			return number.String()
		}
	}
	return ""
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

var _ providers.Factory = (*Factory)(nil)
var _ providers.Provider = (*Provider)(nil)
