package gcp

import (
	"context"

	"google.golang.org/api/compute/v1"
	"google.golang.org/api/option"
)

type computeClient interface {
	AggregatedList(context.Context, string, string) (*compute.InstanceAggregatedList, error)
	Get(context.Context, string, string, string) (*compute.Instance, error)
	Start(context.Context, string, string, string) (*compute.Operation, error)
	Stop(context.Context, string, string, string) (*compute.Operation, error)
	Reset(context.Context, string, string, string) (*compute.Operation, error)
}

type clientFactory func(context.Context, []byte) (computeClient, error)

type sdkClient struct{ service *compute.Service }

func defaultClient(ctx context.Context, serviceAccountJSON []byte) (computeClient, error) {
	// Validate here as well so no caller can accidentally select ambient credentials.
	if err := validateServiceAccount(serviceAccountJSON); err != nil {
		return nil, err
	}
	service, err := compute.NewService(ctx,
		option.WithCredentialsJSON(serviceAccountJSON),
		option.WithScopes(compute.ComputeScope),
	)
	if err != nil {
		return nil, invalidConfig("could not initialize GCP client")
	}
	return &sdkClient{service: service}, nil
}

func (client *sdkClient) AggregatedList(ctx context.Context, project, token string) (*compute.InstanceAggregatedList, error) {
	call := client.service.Instances.AggregatedList(project).ReturnPartialSuccess(true)
	if token != "" {
		call = call.PageToken(token)
	}
	return call.Context(ctx).Do()
}

func (client *sdkClient) Get(ctx context.Context, project, zone, instance string) (*compute.Instance, error) {
	return client.service.Instances.Get(project, zone, instance).Context(ctx).Do()
}

func (client *sdkClient) Start(ctx context.Context, project, zone, instance string) (*compute.Operation, error) {
	return client.service.Instances.Start(project, zone, instance).Context(ctx).Do()
}

func (client *sdkClient) Stop(ctx context.Context, project, zone, instance string) (*compute.Operation, error) {
	return client.service.Instances.Stop(project, zone, instance).Context(ctx).Do()
}

func (client *sdkClient) Reset(ctx context.Context, project, zone, instance string) (*compute.Operation, error) {
	return client.service.Instances.Reset(project, zone, instance).Context(ctx).Do()
}
