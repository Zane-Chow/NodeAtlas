package aws

import (
	"context"
	"encoding/json"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/stretchr/testify/require"

	"controlpanel/internal/providers"
)

func TestProviderPaginatesRegionsAndMapsInstances(t *testing.T) {
	clients := map[string]*fakeEC2{
		"us-east-1": {region: "us-east-1", pages: map[string]*ec2.DescribeInstancesOutput{
			"":     {Reservations: []types.Reservation{{Instances: []types.Instance{instance("i-running", "api", types.InstanceStateNameRunning)}}}, NextToken: awssdk.String("next")},
			"next": {Reservations: []types.Reservation{{Instances: []types.Instance{instance("i-stopped", "worker", types.InstanceStateNameStopped)}}}},
		}},
		"eu-west-1": {region: "eu-west-1", pages: map[string]*ec2.DescribeInstancesOutput{
			"": {Reservations: []types.Reservation{{Instances: []types.Instance{instance("i-pending", "queue", types.InstanceStateNamePending)}}}},
		}},
	}
	factory := newFactory(func(region string, _ staticCredentials) (ec2API, error) { return clients[region], nil })
	provider, err := factory.Create(providers.ConnectionConfig{
		ID: "aws-one", Type: "aws", Settings: json.RawMessage(`{"regions":["us-east-1","eu-west-1"]}`),
		Credentials: json.RawMessage(`{"access_key_id":"AKIATEST","secret_access_key":"secret-value"}`),
	})
	require.NoError(t, err)

	first, err := provider.ListServers(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, first.Servers, 1)
	require.Equal(t, "api", first.Servers[0].Name)
	require.Equal(t, "us-east-1", first.Servers[0].Scope)
	require.Equal(t, providers.StateRunning, first.Servers[0].State)
	require.True(t, first.Servers[0].Capabilities.CanStop.Available)
	require.False(t, first.Servers[0].Capabilities.CanOpenConsoleWindow.Available)
	require.NotNil(t, first.Next)

	second, err := provider.ListServers(context.Background(), first.Next)
	require.NoError(t, err)
	require.Equal(t, "i-stopped", second.Servers[0].ExternalID)
	require.True(t, second.Servers[0].Capabilities.CanStart.Available)
	require.NotNil(t, second.Next)

	third, err := provider.ListServers(context.Background(), second.Next)
	require.NoError(t, err)
	require.Equal(t, "eu-west-1", third.Servers[0].Scope)
	require.Nil(t, third.Next)
}

func TestProviderGetsAndPowersInstanceAndBuildsPortal(t *testing.T) {
	client := &fakeEC2{region: "us-east-1", pages: map[string]*ec2.DescribeInstancesOutput{
		"instance:i-123": {Reservations: []types.Reservation{{Instances: []types.Instance{instance("i-123", "database", types.InstanceStateNameStopped)}}}},
	}}
	factory := newFactory(func(string, staticCredentials) (ec2API, error) { return client, nil })
	provider, err := factory.Create(providers.ConnectionConfig{
		ID: "aws-one", Type: "aws", Settings: json.RawMessage(`{"regions":["us-east-1"]}`),
		Credentials: json.RawMessage(`{"access_key_id":"AKIATEST","secret_access_key":"secret-value","session_token":"session-value"}`),
	})
	require.NoError(t, err)

	server, err := provider.GetServer(context.Background(), providers.ServerRef{ExternalID: "i-123", Scope: "us-east-1"})
	require.NoError(t, err)
	require.Equal(t, "database", server.Name)
	_, err = provider.StartServer(context.Background(), providers.ServerRef{ExternalID: "i-123", Scope: "us-east-1"})
	require.NoError(t, err)
	_, err = provider.StopServer(context.Background(), providers.ServerRef{ExternalID: "i-123", Scope: "us-east-1"})
	require.NoError(t, err)
	_, err = provider.RebootServer(context.Background(), providers.ServerRef{ExternalID: "i-123", Scope: "us-east-1"})
	require.NoError(t, err)
	require.Equal(t, []string{"start:i-123", "stop:i-123", "reboot:i-123"}, client.actions)

	portal, err := provider.ProviderPortalURL(context.Background(), providers.ServerRef{ExternalID: "i-123", Scope: "us-east-1"})
	require.NoError(t, err)
	require.Equal(t, "https://console.aws.amazon.com/ec2/home?region=us-east-1#InstanceDetails:instanceId=i-123", portal.String())
	_, err = provider.OpenConsole(context.Background(), providers.ServerRef{ExternalID: "i-123", Scope: "us-east-1"}, providers.ConsoleWindow)
	require.Error(t, err)
}

func TestFactoryRejectsInvalidConfigurationWithoutEchoingSecrets(t *testing.T) {
	factory := NewFactory()
	_, err := factory.Create(providers.ConnectionConfig{
		ID: "aws", Type: "aws", Settings: json.RawMessage(`{"regions":["not a region"]}`),
		Credentials: json.RawMessage(`{"access_key_id":"AKIATEST","secret_access_key":"do-not-echo"}`),
	})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "do-not-echo")
}

func TestFactoryKeepsMultipleAWSConnectionsIsolated(t *testing.T) {
	type createdClient struct{ region, accessKey, sessionToken string }
	created := make([]createdClient, 0, 2)
	factory := newFactory(func(region string, secret staticCredentials) (ec2API, error) {
		created = append(created, createdClient{region: region, accessKey: secret.AccessKeyID, sessionToken: secret.SessionToken})
		return &fakeEC2{region: region, pages: map[string]*ec2.DescribeInstancesOutput{}}, nil
	})

	first, err := factory.Create(providers.ConnectionConfig{
		ID: "aws-first", Settings: json.RawMessage(`{"regions":["us-east-1"]}`),
		Credentials: json.RawMessage(`{"access_key_id":"AKIAFIRST","secret_access_key":"first-secret"}`),
	})
	require.NoError(t, err)
	second, err := factory.Create(providers.ConnectionConfig{
		ID: "aws-second", Settings: json.RawMessage(`{"regions":["eu-west-1"]}`),
		Credentials: json.RawMessage(`{"access_key_id":"AKIASECOND","secret_access_key":"second-secret","session_token":"second-session"}`),
	})
	require.NoError(t, err)
	require.NotSame(t, first, second)
	require.Equal(t, []createdClient{
		{region: "us-east-1", accessKey: "AKIAFIRST"},
		{region: "eu-west-1", accessKey: "AKIASECOND", sessionToken: "second-session"},
	}, created)
}

type fakeEC2 struct {
	region  string
	pages   map[string]*ec2.DescribeInstancesOutput
	actions []string
}

func (client *fakeEC2) DescribeInstances(_ context.Context, input *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	key := awssdk.ToString(input.NextToken)
	if len(input.InstanceIds) > 0 {
		key = "instance:" + input.InstanceIds[0]
	}
	return client.pages[key], nil
}

func (client *fakeEC2) StartInstances(_ context.Context, input *ec2.StartInstancesInput, _ ...func(*ec2.Options)) (*ec2.StartInstancesOutput, error) {
	client.actions = append(client.actions, "start:"+input.InstanceIds[0])
	return &ec2.StartInstancesOutput{}, nil
}
func (client *fakeEC2) StopInstances(_ context.Context, input *ec2.StopInstancesInput, _ ...func(*ec2.Options)) (*ec2.StopInstancesOutput, error) {
	client.actions = append(client.actions, "stop:"+input.InstanceIds[0])
	return &ec2.StopInstancesOutput{}, nil
}
func (client *fakeEC2) RebootInstances(_ context.Context, input *ec2.RebootInstancesInput, _ ...func(*ec2.Options)) (*ec2.RebootInstancesOutput, error) {
	client.actions = append(client.actions, "reboot:"+input.InstanceIds[0])
	return &ec2.RebootInstancesOutput{}, nil
}

func instance(id, name string, state types.InstanceStateName) types.Instance {
	return types.Instance{
		InstanceId: awssdk.String(id), InstanceType: types.InstanceTypeT3Micro,
		State: &types.InstanceState{Name: state}, PrivateIpAddress: awssdk.String("10.0.0.10"), PublicIpAddress: awssdk.String("198.51.100.10"),
		Tags: []types.Tag{{Key: awssdk.String("Name"), Value: awssdk.String(name)}},
	}
}
