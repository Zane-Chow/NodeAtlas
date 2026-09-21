package gcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"

	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"

	"controlpanel/internal/providers"
)

type settings struct {
	ProjectID string `json:"project_id"`
}

type credentials struct {
	ServiceAccountJSON json.RawMessage `json:"service_account_json"`
}

type Factory struct{ newClient clientFactory }

type Provider struct {
	projectID string
	client    computeClient
}

var (
	projectIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)
	instancePattern  = regexp.MustCompile(`^[a-z](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	zonePattern      = regexp.MustCompile(`^[a-z][a-z0-9-]*-[a-z0-9]+-[a-z]$`)
)

var (
	_ providers.Factory  = (*Factory)(nil)
	_ providers.Provider = (*Provider)(nil)
)

const consoleReason = "GCP browser serial console requires a Google Console session; use the provider portal"

const googleTokenURI = "https://oauth2.googleapis.com/token"

func NewFactory() *Factory                      { return newFactory(defaultClient) }
func newFactory(factory clientFactory) *Factory { return &Factory{newClient: factory} }
func newProvider(project string, client computeClient) *Provider {
	return &Provider{projectID: project, client: client}
}

func (factory *Factory) Create(config providers.ConnectionConfig) (providers.Provider, error) {
	if factory == nil || factory.newClient == nil || strings.TrimSpace(config.ID) == "" {
		return nil, invalidConfig("GCP connection ID is required")
	}
	var configuration settings
	if err := decodeStrict(config.Settings, &configuration); err != nil || !projectIDPattern.MatchString(configuration.ProjectID) {
		return nil, invalidConfig("invalid GCP project settings")
	}
	var secret credentials
	if err := decodeStrict(config.Credentials, &secret); err != nil {
		return nil, invalidConfig("invalid GCP credentials")
	}
	if err := validateServiceAccount(secret.ServiceAccountJSON); err != nil {
		return nil, err
	}
	client, err := factory.newClient(context.Background(), secret.ServiceAccountJSON)
	if err != nil || client == nil {
		return nil, invalidConfig("could not initialize GCP client")
	}
	return newProvider(configuration.ProjectID, client), nil
}

func validateServiceAccount(raw []byte) error {
	var account struct {
		Type        string `json:"type"`
		ClientEmail string `json:"client_email"`
		PrivateKey  string `json:"private_key"`
		TokenURI    string `json:"token_uri"`
	}
	if err := json.Unmarshal(raw, &account); err != nil || account.Type != "service_account" || strings.TrimSpace(account.ClientEmail) == "" || strings.TrimSpace(account.PrivateKey) == "" || strings.TrimSpace(account.TokenURI) == "" {
		return invalidConfig("GCP credentials require a complete service-account JSON object")
	}
	// Permit only the canonical Google endpoint before constructing the SDK.
	// Exact matching also excludes alternate ports, URL userinfo, query strings,
	// fragments, encoded paths, and caller-selected OAuth destinations.
	if account.TokenURI != googleTokenURI {
		return invalidConfig("GCP service-account token URI must be the supported Google HTTPS OAuth endpoint")
	}
	return nil
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

func invalidConfig(message string) error {
	return &providers.Error{Code: providers.ErrorInvalidConfig, Message: message}
}
func inventoryError() error {
	return &providers.Error{Code: providers.ErrorProvider, Message: "GCP returned incomplete instance inventory", Retryable: true}
}

func (provider *Provider) ValidateConnection(ctx context.Context) (providers.ConnectionInfo, error) {
	if _, err := provider.ListServers(ctx, nil); err != nil {
		return providers.ConnectionInfo{}, err
	}
	return providers.ConnectionInfo{DisplayName: "GCP Compute Engine (" + provider.projectID + ")", Version: "Compute Engine v1"}, nil
}

func (provider *Provider) ListServers(ctx context.Context, cursor *providers.Cursor) (providers.ServerPage, error) {
	token := ""
	if cursor != nil {
		token = cursor.Value
	}
	output, err := provider.client.AggregatedList(ctx, provider.projectID, token)
	if err != nil {
		return providers.ServerPage{}, classifyError(err)
	}
	if output == nil || len(output.Unreachables) > 0 || (output.Warning != nil && !benignWarning(output.Warning.Code)) {
		return providers.ServerPage{}, inventoryError()
	}
	page := providers.ServerPage{Servers: make([]providers.RemoteServer, 0)}
	scopes := make([]string, 0, len(output.Items))
	for scope := range output.Items {
		scopes = append(scopes, scope)
	}
	sort.Strings(scopes)
	for _, scope := range scopes {
		group := output.Items[scope]
		if group.Warning != nil && !benignWarning(group.Warning.Code) {
			return providers.ServerPage{}, inventoryError()
		}
		for _, instance := range group.Instances {
			server, err := normalizeInstance(instance)
			if err != nil {
				return providers.ServerPage{}, err
			}
			page.Servers = append(page.Servers, server)
		}
	}
	if output.NextPageToken != "" {
		page.Next = &providers.Cursor{Value: output.NextPageToken}
	}
	return page, nil
}

func benignWarning(code string) bool { return code == "NO_RESULTS" || code == "NO_RESULTS_ON_PAGE" }

func (provider *Provider) GetServer(ctx context.Context, ref providers.ServerRef) (providers.RemoteServer, error) {
	if err := validateRef(ref); err != nil {
		return providers.RemoteServer{}, err
	}
	instance, err := provider.client.Get(ctx, provider.projectID, ref.Scope, ref.ExternalID)
	if err != nil {
		return providers.RemoteServer{}, classifyError(err)
	}
	return normalizeInstance(instance)
}

func validateRef(ref providers.ServerRef) error {
	if !instancePattern.MatchString(ref.ExternalID) || len(ref.Scope) > 63 || !zonePattern.MatchString(ref.Scope) {
		return invalidConfig("invalid GCP instance name or zone")
	}
	return nil
}

func normalizeInstance(instance *compute.Instance) (providers.RemoteServer, error) {
	if instance == nil {
		return providers.RemoteServer{}, inventoryError()
	}
	zone := path.Base(instance.Zone)
	if err := validateRef(providers.ServerRef{ExternalID: instance.Name, Scope: zone}); err != nil {
		return providers.RemoteServer{}, inventoryError()
	}
	state := mapState(instance.Status)
	preemptible, model := false, ""
	if instance.Scheduling != nil {
		preemptible = instance.Scheduling.Preemptible
		model = instance.Scheduling.ProvisioningModel
	}
	machineType := ""
	if instance.MachineType != "" {
		machineType = path.Base(instance.MachineType)
	}
	spec, _ := json.Marshal(map[string]any{"numeric_id": instance.Id, "machine_type": machineType, "labels": instance.Labels, "preemptible": preemptible, "provisioning_model": model})
	addresses := make([]map[string]string, 0)
	add := func(kind, address string) {
		if address != "" {
			addresses = append(addresses, map[string]string{"type": kind, "address": address})
		}
	}
	for _, network := range instance.NetworkInterfaces {
		if network == nil {
			continue
		}
		add("private", network.NetworkIP)
		add("private", network.Ipv6Address)
		for _, access := range network.AccessConfigs {
			if access != nil {
				add("public", access.NatIP)
				add("public", access.ExternalIpv6)
			}
		}
		for _, access := range network.Ipv6AccessConfigs {
			if access != nil {
				add("public", access.ExternalIpv6)
			}
		}
	}
	encodedAddresses, _ := json.Marshal(addresses)
	return providers.RemoteServer{ExternalID: instance.Name, Name: instance.Name, Scope: zone, State: state, RemoteState: instance.Status, Spec: spec, Addresses: encodedAddresses, Capabilities: providers.Capabilities{
		CanStart:             capability(state == providers.StateStopped, "instance must be stopped"),
		CanStop:              capability(state == providers.StateRunning, "instance must be running"),
		CanReboot:            capability(state == providers.StateRunning, "instance must be running"),
		CanEmbedConsole:      providers.Capability{Reason: consoleReason},
		CanOpenConsoleWindow: providers.Capability{Reason: consoleReason},
		HasProviderPortal:    providers.Capability{Available: true},
	}}, nil
}

func capability(available bool, reason string) providers.Capability {
	if available {
		return providers.Capability{Available: true}
	}
	return providers.Capability{Reason: reason}
}
func mapState(state string) providers.ServerState {
	switch state {
	case "PROVISIONING", "STAGING":
		return providers.StatePending
	case "RUNNING":
		return providers.StateRunning
	case "STOPPING", "SUSPENDING":
		return providers.StateStopping
	case "SUSPENDED":
		return providers.StateSuspended
	case "TERMINATED":
		return providers.StateStopped
	default:
		return providers.StateUnknown
	}
}

func (provider *Provider) StartServer(ctx context.Context, ref providers.ServerRef) (providers.ActionReceipt, error) {
	return provider.runAction(ctx, ref, provider.client.Start)
}

func (provider *Provider) StopServer(ctx context.Context, ref providers.ServerRef) (providers.ActionReceipt, error) {
	return provider.runAction(ctx, ref, provider.client.Stop)
}

func (provider *Provider) RebootServer(ctx context.Context, ref providers.ServerRef) (providers.ActionReceipt, error) {
	// Compute Engine reset is a hard reset, not a guest OS restart.
	return provider.runAction(ctx, ref, provider.client.Reset)
}

func (provider *Provider) runAction(ctx context.Context, ref providers.ServerRef, action func(context.Context, string, string, string) (*compute.Operation, error)) (providers.ActionReceipt, error) {
	if err := validateRef(ref); err != nil {
		return providers.ActionReceipt{}, err
	}
	operation, err := action(ctx, provider.projectID, ref.Scope, ref.ExternalID)
	if err != nil {
		return providers.ActionReceipt{}, classifyError(err)
	}
	if operation == nil || operation.Name == "" {
		return providers.ActionReceipt{}, &providers.Error{Code: providers.ErrorProvider, Message: "GCP returned an invalid operation"}
	}
	return providers.ActionReceipt{RequestID: operation.Name}, nil
}

func (provider *Provider) OpenConsole(context.Context, providers.ServerRef, providers.ConsoleMode) (providers.ConsoleTarget, error) {
	return providers.ConsoleTarget{}, &providers.Error{Code: providers.ErrorUnsupported, Message: consoleReason}
}

func (provider *Provider) ProviderPortalURL(_ context.Context, ref providers.ServerRef) (*url.URL, error) {
	if err := validateRef(ref); err != nil {
		return nil, err
	}
	return &url.URL{
		Scheme:   "https",
		Host:     "console.cloud.google.com",
		Path:     path.Join("/compute/instancesDetail/zones", ref.Scope, "instances", ref.ExternalID),
		RawQuery: url.Values{"project": {provider.projectID}}.Encode(),
	}, nil
}

func classifyError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &providers.Error{Code: providers.ErrorNetwork, Message: "GCP request was canceled or timed out", Retryable: true}
	}
	var apiError *googleapi.Error
	if errors.As(err, &apiError) {
		switch apiError.Code {
		case 401:
			return &providers.Error{Code: providers.ErrorAuthentication, Message: "GCP credentials were rejected"}
		case 403:
			for _, detail := range apiError.Errors {
				switch detail.Reason {
				case "rateLimitExceeded", "userRateLimitExceeded", "servingLimitExceeded":
					return &providers.Error{Code: providers.ErrorRateLimited, Message: "GCP request was rate limited", Retryable: true}
				}
			}
			return &providers.Error{Code: providers.ErrorPermission, Message: "GCP permission was denied"}
		case 404:
			return &providers.Error{Code: providers.ErrorNotFound, Message: "GCP instance was not found"}
		case 409, 412:
			return &providers.Error{Code: providers.ErrorProvider, Message: "GCP instance state conflicts with the requested action"}
		case 429:
			return &providers.Error{Code: providers.ErrorRateLimited, Message: "GCP request was rate limited", Retryable: true}
		default:
			return &providers.Error{Code: providers.ErrorProvider, Message: "GCP request failed", Retryable: apiError.Code >= 500 && apiError.Code <= 599}
		}
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		return &providers.Error{Code: providers.ErrorNetwork, Message: "GCP network request failed", Retryable: true}
	}
	return &providers.Error{Code: providers.ErrorProvider, Message: "GCP request failed"}
}
