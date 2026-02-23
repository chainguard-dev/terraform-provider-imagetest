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
	"go.opentelemetry.io/otel/trace"
)

type driver struct {
	region        string
	executionRole string
	functionName  string

	client *lambda.Client
}

// NewDriver creates a new driver for AWS Lambda.
//
// This isn't used by the typical imagetest_tests resource, but is instead used by
// the imagetest_tests_lambda resource. It satisfies the same interface anyway.
func NewDriver(region, executionRole string) (drivers.Tester, error) {
	return &driver{
		region:        region,
		executionRole: executionRole,
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

	k.functionName = fmt.Sprintf("imagetest-%s-%d", dig.DigestStr()[8:16], time.Now().UnixNano())
	if _, err := k.client.CreateFunction(ctx, &lambda.CreateFunctionInput{
		FunctionName: &k.functionName,
		Code:         &types.FunctionCode{ImageUri: &[]string{ref.String()}[0]},
		PackageType:  types.PackageTypeImage,
		Role:         &k.executionRole,
		Publish:      true,
	}); err != nil {
		return nil, fmt.Errorf("creating Lambda function: %w", err)
	}
	clog.FromContext(ctx).Info("Created Lambda function", "name", k.functionName)
	span := trace.SpanFromContext(ctx)
	span.AddEvent("lambda.function.created")

	var out *lambda.GetFunctionOutput
L:
	for range 10 {
		var err error
		out, err = k.client.GetFunction(ctx, &lambda.GetFunctionInput{
			FunctionName: &k.functionName,
		})
		if err != nil {
			return nil, fmt.Errorf("getting Lambda function: %w", err)
		}
		switch out.Configuration.State {
		case types.StatePending, types.StateInactive:
			time.Sleep(5 * time.Second)
		case types.StateFailed:
			return nil, fmt.Errorf("function failed: %s", *out.Configuration.StateReason)
		case types.StateActive:
			break L
		}
		clog.FromContext(ctx).Info("Waiting for Lambda function to be active", "state", out.Configuration.State)
	}
	if out.Configuration.State != types.StateActive {
		return nil, fmt.Errorf("function state is %s: %s", out.Configuration.State, *out.Configuration.StateReason)
	}
	clog.FromContext(ctx).Info("Lambda function is active", "name", k.functionName)
	span.AddEvent("lambda.function.active")

	// Invoke the function to ensure it is ready.
	if out, err := k.client.Invoke(ctx, &lambda.InvokeInput{FunctionName: &k.functionName}); err != nil {
		return nil, fmt.Errorf("failed to invoke Lambda function: %w", err)
	} else if out.StatusCode != 200 {
		return nil, fmt.Errorf("function returned %d: %s", out.StatusCode, string(out.Payload))
	} else if out.FunctionError != nil {
		return nil, fmt.Errorf("function returned error: %q: %s", *out.FunctionError, string(out.Payload))
	} else {
		if out == nil {
			return nil, fmt.Errorf("function returned nil output")
		}
		clog.FromContext(ctx).Info("function invoked successfully", "out", out)
	}
	return nil, nil
}
