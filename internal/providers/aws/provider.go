package aws

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	awscredentials "github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
	smithymiddleware "github.com/aws/smithy-go/middleware"

	"controlpanel/internal/providers"
)

type ec2API interface {
	DescribeInstances(context.Context, *ec2.DescribeInstancesInput, ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
	StartInstances(context.Context, *ec2.StartInstancesInput, ...func(*ec2.Options)) (*ec2.StartInstancesOutput, error)
	StopInstances(context.Context, *ec2.StopInstancesInput, ...func(*ec2.Options)) (*ec2.StopInstancesOutput, error)
	RebootInstances(context.Context, *ec2.RebootInstancesInput, ...func(*ec2.Options)) (*ec2.RebootInstancesOutput, error)
}

type staticCredentials struct {
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	SessionToken    string `json:"session_token,omitempty"`
}

type settings struct {
	Regions []string `json:"regions"`
}

type clientFactory func(string, staticCredentials) (ec2API, error)

type Factory struct{ newClient clientFactory }

type Provider struct {
	regions []string
	clients map[string]ec2API
}

type cursorPosition struct {
	Region int    `json:"region"`
	Token  string `json:"token,omitempty"`
}

var regionPattern = regexp.MustCompile(`^[a-z]{2}(?:-[a-z0-9]+){1,3}-[0-9]+$`)

func NewFactory() *Factory { return newFactory(defaultClient) }

func newFactory(factory clientFactory) *Factory { return &Factory{newClient: factory} }

func defaultClient(region string, secret staticCredentials) (ec2API, error) {
	httpClient := &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2: true, MaxIdleConns: 20, IdleConnTimeout: 60 * time.Second, TLSHandshakeTimeout: 10 * time.Second,
	}}
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(awscredentials.NewStaticCredentialsProvider(secret.AccessKeyID, secret.SecretAccessKey, secret.SessionToken)),
		awsconfig.WithHTTPClient(httpClient),
	)
	if err != nil {
		return nil, errors.New("initialize AWS client")
	}
	return ec2.NewFromConfig(cfg), nil
}

func (factory *Factory) Create(config providers.ConnectionConfig) (providers.Provider, error) {
	if factory == nil || factory.newClient == nil || strings.TrimSpace(config.ID) == "" {
		return nil, errors.New("AWS connection ID is required")
	}
	var configuration settings
	if err := decodeStrict(config.Settings, &configuration); err != nil {
		return nil, errors.New("invalid AWS settings")
	}
	regions, err := normalizeRegions(configuration.Regions)
	if err != nil {
		return nil, err
	}
	var secret staticCredentials
	if err := decodeStrict(config.Credentials, &secret); err != nil || strings.TrimSpace(secret.AccessKeyID) == "" || strings.TrimSpace(secret.SecretAccessKey) == "" {
		return nil, errors.New("invalid AWS credentials")
	}
	secret.AccessKeyID = strings.TrimSpace(secret.AccessKeyID)
	secret.SecretAccessKey = strings.TrimSpace(secret.SecretAccessKey)
	secret.SessionToken = strings.TrimSpace(secret.SessionToken)
	clients := make(map[string]ec2API, len(regions))
	for _, region := range regions {
		client, err := factory.newClient(region, secret)
		if err != nil {
			return nil, errors.New("initialize AWS region client")
		}
		clients[region] = client
	}
	return &Provider{regions: regions, clients: clients}, nil
}

func normalizeRegions(values []string) ([]string, error) {
	if len(values) == 0 || len(values) > 32 {
		return nil, errors.New("AWS settings require between 1 and 32 regions")
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		region := strings.ToLower(strings.TrimSpace(value))
		if !regionPattern.MatchString(region) || len(region) > 64 {
			return nil, errors.New("AWS settings contain an invalid region")
		}
		if _, duplicate := seen[region]; duplicate {
			continue
		}
		seen[region] = struct{}{}
		result = append(result, region)
	}
	return result, nil
}

func (provider *Provider) ValidateConnection(ctx context.Context) (providers.ConnectionInfo, error) {
	for _, region := range provider.regions {
		if _, err := provider.clients[region].DescribeInstances(ctx, &ec2.DescribeInstancesInput{MaxResults: awssdk.Int32(5)}); err != nil {
			return providers.ConnectionInfo{}, classifyError(err)
		}
	}
	return providers.ConnectionInfo{DisplayName: fmt.Sprintf("AWS EC2 (%d regions)", len(provider.regions)), Version: "AWS Query API"}, nil
}

func (provider *Provider) ListServers(ctx context.Context, cursor *providers.Cursor) (providers.ServerPage, error) {
	position, err := decodeCursor(cursor)
	if err != nil || position.Region < 0 || position.Region >= len(provider.regions) {
		return providers.ServerPage{}, &providers.Error{Code: providers.ErrorInvalidConfig, Message: "invalid AWS inventory cursor"}
	}
	region := provider.regions[position.Region]
	input := &ec2.DescribeInstancesInput{MaxResults: awssdk.Int32(100)}
	if position.Token != "" {
		input.NextToken = awssdk.String(position.Token)
	}
	output, err := provider.clients[region].DescribeInstances(ctx, input)
	if err != nil {
		return providers.ServerPage{}, classifyError(err)
	}
	page := providers.ServerPage{Servers: flattenInstances(output, region)}
	next := cursorPosition{Region: position.Region, Token: awssdk.ToString(output.NextToken)}
	if next.Token == "" {
		next.Region++
	}
	if next.Region < len(provider.regions) {
		page.Next = &providers.Cursor{Value: encodeCursor(next)}
	}
	return page, nil
}

func (provider *Provider) GetServer(ctx context.Context, ref providers.ServerRef) (providers.RemoteServer, error) {
	client, ok := provider.clients[ref.Scope]
	if !ok || strings.TrimSpace(ref.ExternalID) == "" {
		return providers.RemoteServer{}, &providers.Error{Code: providers.ErrorNotFound, Message: "AWS instance was not found"}
	}
	output, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{ref.ExternalID}})
	if err != nil {
		return providers.RemoteServer{}, classifyError(err)
	}
	servers := flattenInstances(output, ref.Scope)
	if len(servers) != 1 {
		return providers.RemoteServer{}, &providers.Error{Code: providers.ErrorNotFound, Message: "AWS instance was not found"}
	}
	return servers[0], nil
}

func (provider *Provider) StartServer(ctx context.Context, ref providers.ServerRef) (providers.ActionReceipt, error) {
	client, err := provider.client(ref)
	if err != nil {
		return providers.ActionReceipt{}, err
	}
	output, callErr := client.StartInstances(ctx, &ec2.StartInstancesInput{InstanceIds: []string{ref.ExternalID}})
	if callErr != nil {
		return providers.ActionReceipt{}, classifyError(callErr)
	}
	return providers.ActionReceipt{RequestID: requestID(output.ResultMetadata)}, nil
}

func (provider *Provider) StopServer(ctx context.Context, ref providers.ServerRef) (providers.ActionReceipt, error) {
	client, err := provider.client(ref)
	if err != nil {
		return providers.ActionReceipt{}, err
	}
	output, callErr := client.StopInstances(ctx, &ec2.StopInstancesInput{InstanceIds: []string{ref.ExternalID}})
	if callErr != nil {
		return providers.ActionReceipt{}, classifyError(callErr)
	}
	return providers.ActionReceipt{RequestID: requestID(output.ResultMetadata)}, nil
}

func (provider *Provider) RebootServer(ctx context.Context, ref providers.ServerRef) (providers.ActionReceipt, error) {
	client, err := provider.client(ref)
	if err != nil {
		return providers.ActionReceipt{}, err
	}
	output, callErr := client.RebootInstances(ctx, &ec2.RebootInstancesInput{InstanceIds: []string{ref.ExternalID}})
	if callErr != nil {
		return providers.ActionReceipt{}, classifyError(callErr)
	}
	return providers.ActionReceipt{RequestID: requestID(output.ResultMetadata)}, nil
}

func requestID(metadata smithymiddleware.Metadata) string {
	value, _ := awsmiddleware.GetRequestIDMetadata(metadata)
	return value
}

func (provider *Provider) OpenConsole(context.Context, providers.ServerRef, providers.ConsoleMode) (providers.ConsoleTarget, error) {
	return providers.ConsoleTarget{}, &providers.Error{Code: providers.ErrorUnsupported, Message: "AWS browser serial console requires an AWS Console session; use the provider portal"}
}

func (provider *Provider) ProviderPortalURL(_ context.Context, ref providers.ServerRef) (*url.URL, error) {
	if _, err := provider.client(ref); err != nil {
		return nil, err
	}
	host := "console.aws.amazon.com"
	if strings.HasPrefix(ref.Scope, "cn-") {
		host = "console.amazonaws.cn"
	} else if strings.HasPrefix(ref.Scope, "us-gov-") {
		host = "console.amazonaws-us-gov.com"
	}
	return &url.URL{Scheme: "https", Host: host, Path: "/ec2/home", RawQuery: "region=" + url.QueryEscape(ref.Scope), Fragment: "InstanceDetails:instanceId=" + ref.ExternalID}, nil
}

func (provider *Provider) client(ref providers.ServerRef) (ec2API, error) {
	client, ok := provider.clients[ref.Scope]
	if !ok || strings.TrimSpace(ref.ExternalID) == "" {
		return nil, &providers.Error{Code: providers.ErrorNotFound, Message: "AWS instance was not found"}
	}
	return client, nil
}

func flattenInstances(output *ec2.DescribeInstancesOutput, region string) []providers.RemoteServer {
	if output == nil {
		return nil
	}
	result := make([]providers.RemoteServer, 0)
	for _, reservation := range output.Reservations {
		for _, instance := range reservation.Instances {
			if server, ok := normalizeInstance(instance, region); ok {
				result = append(result, server)
			}
		}
	}
	return result
}

func normalizeInstance(instance types.Instance, region string) (providers.RemoteServer, bool) {
	id := strings.TrimSpace(awssdk.ToString(instance.InstanceId))
	if id == "" {
		return providers.RemoteServer{}, false
	}
	rawState := "unknown"
	if instance.State != nil {
		rawState = string(instance.State.Name)
	}
	state := mapState(rawState)
	name := id
	for _, tag := range instance.Tags {
		if awssdk.ToString(tag.Key) == "Name" && strings.TrimSpace(awssdk.ToString(tag.Value)) != "" {
			name = strings.TrimSpace(awssdk.ToString(tag.Value))
			break
		}
	}
	spec, _ := json.Marshal(map[string]any{"instance_type": string(instance.InstanceType), "architecture": string(instance.Architecture), "platform_details": awssdk.ToString(instance.PlatformDetails)})
	addresses := make([]map[string]string, 0, 2)
	if value := awssdk.ToString(instance.PrivateIpAddress); value != "" {
		addresses = append(addresses, map[string]string{"type": "private", "address": value})
	}
	if value := awssdk.ToString(instance.PublicIpAddress); value != "" {
		addresses = append(addresses, map[string]string{"type": "public", "address": value})
	}
	encodedAddresses, _ := json.Marshal(addresses)
	capabilities := providers.Capabilities{
		CanStart:          capability(state == providers.StateStopped, "instance must be stopped"),
		CanStop:           capability(state == providers.StateRunning, "instance must be running"),
		CanReboot:         capability(state == providers.StateRunning, "instance must be running"),
		HasProviderPortal: providers.Capability{Available: true},
		CanEmbedConsole:   providers.Capability{Reason: "AWS serial console is not embedded by this connection type"},
		CanOpenConsoleWindow: providers.Capability{
			Reason: "AWS browser serial console requires an AWS Console session; use the provider portal",
		},
	}
	return providers.RemoteServer{ExternalID: id, Scope: region, Name: name, State: state, RemoteState: rawState, Spec: spec, Addresses: encodedAddresses, Capabilities: capabilities}, true
}

func capability(available bool, reason string) providers.Capability {
	if available {
		return providers.Capability{Available: true}
	}
	return providers.Capability{Reason: reason}
}

func mapState(state string) providers.ServerState {
	switch state {
	case string(types.InstanceStateNamePending):
		return providers.StatePending
	case string(types.InstanceStateNameRunning):
		return providers.StateRunning
	case string(types.InstanceStateNameStopping), string(types.InstanceStateNameShuttingDown):
		return providers.StateStopping
	case string(types.InstanceStateNameStopped):
		return providers.StateStopped
	case string(types.InstanceStateNameTerminated):
		return providers.StateError
	default:
		return providers.StateUnknown
	}
}

func decodeCursor(cursor *providers.Cursor) (cursorPosition, error) {
	if cursor == nil {
		return cursorPosition{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor.Value)
	if err != nil {
		return cursorPosition{}, err
	}
	var position cursorPosition
	if err := json.Unmarshal(raw, &position); err != nil {
		return cursorPosition{}, err
	}
	return position, nil
}

func encodeCursor(position cursorPosition) string {
	raw, _ := json.Marshal(position)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeStrict(raw []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("JSON must contain one value")
	}
	return nil
}

func classifyError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return &providers.Error{Code: providers.ErrorNetwork, Message: "AWS request timed out", Retryable: true}
	}
	var apiError smithy.APIError
	if errors.As(err, &apiError) {
		switch apiError.ErrorCode() {
		case "AuthFailure", "InvalidClientTokenId", "SignatureDoesNotMatch", "UnrecognizedClientException":
			return &providers.Error{Code: providers.ErrorAuthentication, Message: "AWS credentials were rejected"}
		case "UnauthorizedOperation", "AccessDenied", "AccessDeniedException":
			return &providers.Error{Code: providers.ErrorPermission, Message: "AWS permission was denied"}
		case "RequestLimitExceeded", "Throttling", "ThrottlingException":
			return &providers.Error{Code: providers.ErrorRateLimited, Message: "AWS request was rate limited", Retryable: true}
		case "InvalidInstanceID.NotFound":
			return &providers.Error{Code: providers.ErrorNotFound, Message: "AWS instance was not found"}
		default:
			return &providers.Error{Code: providers.ErrorProvider, Message: "AWS request failed"}
		}
	}
	return &providers.Error{Code: providers.ErrorNetwork, Message: "AWS network request failed", Retryable: true}
}
