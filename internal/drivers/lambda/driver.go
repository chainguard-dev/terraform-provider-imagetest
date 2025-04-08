package lambda

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
	"github.com/google/go-containerregistry/pkg/name"
)

type driver struct {
	name         string
	region       string
	functionName string

	client *lambda.Client
}

func NewDriver(n string) (drivers.Tester, error) {
	return &driver{
		name:   n,
		region: "us-west-2",
	}, nil
}

func (k *driver) Setup(ctx context.Context) error {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(k.region))
	if err != nil {
		return fmt.Errorf("loading AWS config: %w", err)
	}
	k.client = lambda.NewFromConfig(cfg)
	return nil
}

func (k *driver) Teardown(ctx context.Context) error {
	if v := os.Getenv("IMAGETEST_LAMBDA_SKIP_TEARDOWN"); v == "true" {
		clog.FromContext(ctx).Info("Skipping Lambda teardown due to IMAGETEST_LAMBDA_SKIP_TEARDOWN=true")
		return nil
	}

	if _, err := k.client.DeleteFunction(ctx, &lambda.DeleteFunctionInput{
		FunctionName: &k.functionName,
	}); err != nil {
		return fmt.Errorf("deleting Lambda function: %w", err)
	}
	clog.FromContext(ctx).Info("Deleted Lambda function", "name", k.functionName)
	return nil
}

func (k *driver) Run(ctx context.Context, ref name.Reference) (*drivers.RunResult, error) {
	dig, ok := ref.(name.Digest)
	if !ok {
		return nil, fmt.Errorf("expected digest reference, got %T %q", ref, ref)
	}

	// TODO: ensure a minimal role `lambda-ex`

	k.functionName = fmt.Sprintf("imagetest-%s-%d", dig.DigestStr()[8:16], time.Now().Unix())
	if _, err := k.client.CreateFunction(ctx, &lambda.CreateFunctionInput{
		FunctionName: &k.functionName,
		Code:         &types.FunctionCode{ImageUri: &[]string{ref.String()}[0]},
		PackageType:  types.PackageTypeImage,
		Role:         &[]string{os.Getenv("IMAGETEST_LAMBDA_ROLE")}[0], // TODO remove this
		Publish:      true,
	}); err != nil {
		return nil, fmt.Errorf("creating Lambda function: %w", err)
	}
	clog.FromContext(ctx).Info("Created Lambda function", "name", k.functionName)

	for range 10 {
		out, err := k.client.GetFunction(ctx, &lambda.GetFunctionInput{
			FunctionName: &k.functionName,
		})
		if err != nil {
			return nil, fmt.Errorf("getting Lambda function: %w", err)
		}
		if out.Configuration.State == types.StateActive {
			break
		}
		clog.FromContext(ctx).Info("Waiting for Lambda function to be active", "state", out.Configuration.State)
		time.Sleep(5 * time.Second)
	}
	clog.FromContext(ctx).Info("Lambda function is active", "name", k.functionName)

	// Invoke the function to ensure it is ready.
	if out, err := k.client.Invoke(ctx, &lambda.InvokeInput{FunctionName: &k.functionName}); err != nil {
		return nil, fmt.Errorf("failed to invoke Lambda function: %w", err)
	} else if out.StatusCode != 200 {
		return nil, fmt.Errorf("function returned %d: %s", out.StatusCode, string(out.Payload))
	} else if out.FunctionError != nil {
		return nil, fmt.Errorf("function returned error: %q", *out.FunctionError)
	} else {
		if out == nil {
			return nil, fmt.Errorf("function returned nil output")
		}
		clog.FromContext(ctx).Info("function invoked successfully", "out", out)
	}
	return nil, nil
}
