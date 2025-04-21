package provider

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers/lambda"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/provider/framework"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.ResourceWithConfigure = &TestsResource{}

func NewTestsLambdaResource() resource.Resource {
	return &TestsLambdaResource{WithTypeName: "tests_lambda"}
}

type TestsLambdaResource struct {
	framework.WithTypeName
	framework.WithNoOpDelete
	framework.WithNoOpRead

	ropts []remote.Option
}

type TestsLambdaResourceModel struct {
	Id            types.String `tfsdk:"id"`
	Name          types.String `tfsdk:"name"`
	ImageRef      types.String `tfsdk:"image_ref"`
	ExecutionRole types.String `tfsdk:"execution_role"`
	Region        types.String `tfsdk:"region"`
}

func (t *TestsLambdaResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: ``,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The unique identifier for the test. If a name is provided, this will be the name appended with a random suffix.",
				Computed:    true,
			},
			"name": schema.StringAttribute{
				Description: "The name of the test. If one is not provided, a random name will be generated.",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("test"),
			},
			"image_ref": schema.StringAttribute{
				Description: "The image ref to deploy and test.",
				Required:    true,
			},
			"execution_role": schema.StringAttribute{
				Description: "The ARN of the IAM role to use for the Lambda function.",
				Required:    true,
			},
			"region": schema.StringAttribute{
				Description: "The AWS region to deploy the test in. If not provided, the default region will be used.",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("us-west-2"),
			},
		},
	}
}

func (t *TestsLambdaResource) Configure(context.Context, resource.ConfigureRequest, *resource.ConfigureResponse) {
}

func (t *TestsLambdaResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data TestsLambdaResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if data.Name.IsNull() {
		data.Name = types.StringValue(uuid.New().String())
	}

	resp.Diagnostics.Append(t.do(ctx, &data)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (t *TestsLambdaResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data TestsLambdaResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(t.do(ctx, &data)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (t *TestsLambdaResource) do(ctx context.Context, data *TestsLambdaResourceModel) (ds diag.Diagnostics) {
	ctx = clog.WithValues(ctx, "test_id", data.Id.ValueString())

	ref, err := name.NewDigest(data.ImageRef.ValueString())
	if err != nil {
		return []diag.Diagnostic{diag.NewErrorDiagnostic("failed to parse image digest", err.Error())}
	}

	h := sha256.New()
	_, _ = fmt.Fprint(h, data.Name.ValueString())
	_, _ = fmt.Fprint(h, ref.DigestStr())
	data.Id = types.StringValue(fmt.Sprintf("%x", h.Sum(nil)))

	t.ropts = append(t.ropts, remote.WithContext(ctx))

	dr, err := lambda.NewDriver(data.ImageRef.ValueString(), data.Region.ValueString(), data.ExecutionRole.ValueString())
	if err != nil {
		return []diag.Diagnostic{diag.NewErrorDiagnostic("failed to create driver", err.Error())}
	}

	defer func() {
		if teardownErr := t.maybeTeardown(ctx, dr, ds.HasError()); teardownErr != nil {
			ds = append(ds, teardownErr)
		}
	}()

	clog.InfoContext(ctx, "setting up driver")
	if err := dr.Setup(ctx); err != nil {
		ds = []diag.Diagnostic{diag.NewErrorDiagnostic("failed to setup driver", err.Error())}
		return
	}

	if _, err := dr.Run(ctx, ref); err != nil {
		ds = []diag.Diagnostic{diag.NewErrorDiagnostic("test failed", err.Error())}
		return
	}
	return ds
}

func (t *TestsLambdaResource) maybeTeardown(ctx context.Context, d drivers.Tester, failed bool) diag.Diagnostic {
	if v := os.Getenv("IMAGETEST_LAMBDA_SKIP_TEARDOWN"); v != "" {
		return diag.NewWarningDiagnostic("skipping teardown", "IMAGETEST_SKIP_TEARDOWN is set, skipping teardown")
	}

	if v := os.Getenv("IMAGETEST_LAMBDA_SKIP_TEARDOWN_ON_FAILURE"); v != "" && failed {
		return diag.NewWarningDiagnostic("skipping teardown", "IMAGETEST_SKIP_TEARDOWN_ON_FAILURE is set and test failed, skipping teardown")
	}

	if err := d.Teardown(ctx); err != nil {
		return diag.NewWarningDiagnostic("failed to teardown lambda test driver", err.Error())
	}

	return nil
}
