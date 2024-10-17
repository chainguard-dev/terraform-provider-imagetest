package provider

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/docker"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/provider/framework"
	"github.com/docker/docker/api/types/mount"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

const (
	// TODO: Make the default feature timeout configurable?
	defaultTestDockerRunCreateTimeout = 15 * time.Minute
)

var _ resource.Resource = &TestDockerRunResource{}

func NewTestDockerRunResource() resource.Resource {
	return &TestDockerRunResource{WithTypeName: "test_docker_run"}
}

// TestDockerRunResource defines the resource implementation.
type TestDockerRunResource struct {
	framework.WithTypeName
	framework.WithNoOpRead
	framework.WithNoOpDelete

	store *ProviderStore
}

type TestResult string

const (
	TestResultPass TestResult = "PASS"
	TestResultFail TestResult = "FAIL"
)

// TestDockerRunResourceModel describes the resource data model.
type TestDockerRunResourceModel struct {
	Name        types.String   `tfsdk:"name"`
	Description types.String   `tfsdk:"description"`
	Labels      types.Map      `tfsdk:"labels"`
	Timeouts    timeouts.Value `tfsdk:"timeouts"`
	Skipped     types.String   `tfsdk:"skipped"`

	Cid        types.String          `tfsdk:"cid"`
	Result     types.String          `tfsdk:"result"`
	Image      types.String          `tfsdk:"image"`
	Entrypoint []string              `tfsdk:"entrypoint"`
	Cmd        []string              `tfsdk:"cmd"`
	Mounts     []ContainerMountModel `tfsdk:"mounts"`
	User       types.String          `tfsdk:"user"`
}

func (r *TestDockerRunResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		// This description is used by the documentation generator and the language server.
		MarkdownDescription: "TestDockerRun resource, used to evaluate the steps of a given test",
		Attributes: mergeResourceSchemas(
			map[string]schema.Attribute{
				"name": schema.StringAttribute{
					Description: "The name of the feature",
					Required:    true,
				},
				"description": schema.StringAttribute{
					Description: "A descriptor of the feature",
					Optional:    true,
				},
				"labels": schema.MapAttribute{
					Description: "A set of labels used to optionally filter execution of the feature",
					Optional:    true,
					ElementType: basetypes.StringType{},
				},
				"timeouts": timeouts.Attributes(ctx, timeouts.Opts{
					Create: true,
				}),
				"skipped": schema.StringAttribute{
					Description: "A computed value that indicates whether or not the feature was skipped. If the test is skipped, this field is populated wth the reason.",
					Computed:    true,
				},
				"image": schema.StringAttribute{
					Description: "The full image reference to use for the container.",
					Required:    true,
				},
				"entrypoint": schema.ListAttribute{
					Description: "The command or set of commands that should be run at this step",
					Optional:    true,
					ElementType: basetypes.StringType{},
				},
				"cmd": schema.ListAttribute{
					Description: "The command or set of commands that should be run at this step",
					Optional:    true,
					ElementType: basetypes.StringType{},
				},
				"mounts": schema.ListNestedAttribute{
					Description: "The list of mounts to create on the container.",
					Optional:    true,
					NestedObject: schema.NestedAttributeObject{
						Attributes: map[string]schema.Attribute{
							"source": schema.StringAttribute{
								Description: "The relative or absolute path on the host to the source directory to mount.",
								Required:    true,
							},
							"destination": schema.StringAttribute{
								Description: "The absolute path on the container to mount the source directory.",
								Required:    true,
							},
							"read_only": schema.BoolAttribute{
								Description: "Whether the mount should be read only.",
								Optional:    true,
								Computed:    true,
								Default:     booldefault.StaticBool(false),
							},
						},
					},
				},
				"user": schema.StringAttribute{
					Description: "The user to run the command as.",
					Optional:    true,
				},
				"cid": schema.StringAttribute{
					Description: "The ID of the container that was created.",
					Computed:    true,
				},
				"result": schema.StringAttribute{
					Description: "The result of the command. This is always either PASS or FAIL.",
					Computed:    true,
				},
			},
		),
	}
}

func (r *TestDockerRunResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	// Prevent panic if the provider has not been configured.
	if req.ProviderData == nil {
		return
	}

	store, ok := req.ProviderData.(*ProviderStore)
	if !ok {
		resp.Diagnostics.AddError("invalid provider data", "...")
		return
	}

	r.store = store
}

func (r *TestDockerRunResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data TestDockerRunResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(r.do(ctx, &data)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
}

func (r *TestDockerRunResource) do(ctx context.Context, data *TestDockerRunResourceModel) (ds diag.Diagnostics) {
	data.Result = types.StringValue(string(TestResultFail))

	labels := make(map[string]string)
	if diag := data.Labels.ElementsAs(ctx, &labels, false); diag.HasError() {
		return diag
	}

	data.Skipped = types.StringValue(skippedValue(r.store, labels))
	if data.Skipped.ValueString() != "" {
		data.Cid = types.StringValue("")

		// NOTE: "Result" is reserved for downstream dependencies of this resource
		// and should always adhere to either PASS or FAIL. If a downstream
		// resource wants to depend on whether a test was skipped (rarely),
		// intentionally don't use this and use .skipped instead
		data.Result = types.StringValue(string(TestResultPass))

		ds.AddWarning(
			fmt.Sprintf("skipping test %s", data.Name.ValueString()),
			data.Skipped.ValueString(),
		)
		return ds
	}

	timeout, diags := data.Timeouts.Create(ctx, defaultTestDockerRunCreateTimeout)
	if diags.HasError() {
		ds.Append(diags...)
		return ds
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cli, err := docker.New()
	if err != nil {
		return []diag.Diagnostic{diag.NewErrorDiagnostic("failed to create docker client", err.Error())}
	}

	ref, err := name.ParseReference(data.Image.ValueString())
	if err != nil {
		return []diag.Diagnostic{diag.NewErrorDiagnostic("invalid resource input", fmt.Sprintf("invalid image reference: %s", err))}
	}

	out := bytes.Buffer{}

	req := &docker.Request{
		Ref:        ref,
		User:       data.User.ValueString(),
		Entrypoint: data.Entrypoint,
		Cmd:        data.Cmd,
		Mounts:     []mount.Mount{},
		Logger:     &out,
	}

	for _, m := range data.Mounts {
		req.Mounts = append(req.Mounts, mount.Mount{
			Type:     mount.TypeBind,
			Source:   m.Source.ValueString(),
			Target:   m.Destination.ValueString(),
			ReadOnly: m.ReadOnly.ValueBool(),
		})
	}

	cid, err := cli.Run(ctx, req)
	if err != nil {
		return []diag.Diagnostic{diag.NewErrorDiagnostic("failed to start docker container", fmt.Sprintf("%s\n\n%s", err.Error(), out.String()))}
	}
	data.Cid = types.StringValue(cid)
	data.Result = types.StringValue("PASS")

	return ds
}

func (r *TestDockerRunResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data TestDockerRunResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(r.do(ctx, &data)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
}
