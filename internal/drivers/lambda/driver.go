package lambda

import (
	"context"
	"fmt"
	"os"

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
	k.client = lambda.New(lambda.Options{
		Region: k.region,
		// TODO(jason): Add support for FIPS endpoints.
	})
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
	return nil
}

func (k *driver) Run(ctx context.Context, ref name.Reference) error {
	dig, ok := ref.(name.Digest)
	if !ok {
		return fmt.Errorf("expected digest reference, got %T", ref)
	}
	k.functionName = fmt.Sprintf("imagetest-%s", dig.DigestStr()[0:7])
	if _, err := k.client.CreateFunction(ctx, &lambda.CreateFunctionInput{
		FunctionName: &k.functionName,
		// TODO: LoggingConfig
		Code: &types.FunctionCode{ImageUri: &[]string{ref.Identifier()}[0]},
	}); err != nil {
		return fmt.Errorf("creating Lambda function: %w", err)
	}
	// Invoke the function to ensure it is ready.
	if _, err := k.client.Invoke(ctx, &lambda.InvokeInput{FunctionName: &k.functionName}); err != nil {
		return fmt.Errorf("invoking Lambda function: %w", err)
	}
	return nil
}
