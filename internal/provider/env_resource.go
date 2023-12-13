package provider

import (
	"context"
	"fmt"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/envs"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/features"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harnesses"
	itypes "github.com/chainguard-dev/terraform-provider-imagetest/internal/types"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

// Ensure provider defined types fully satisfy framework interfaces.
var (
	_ resource.Resource                = &EnvResource{}
	_ resource.ResourceWithConfigure   = &EnvResource{}
	_ resource.ResourceWithImportState = &EnvResource{}
)

func NewEnvResource() resource.Resource {
	return &EnvResource{}
}

// EnvResource defines the resource implementation.
type EnvResource struct {
	id    string
	store *ProviderStore
}

// EnvResourceModel describes the resource data model.
type EnvResourceModel struct {
	Id        types.String                   `tfsdk:"id"`
	HarnessId types.String                   `tfsdk:"harness"`
	Tests     []EnvironmentTestResourceModel `tfsdk:"test"`
	Labels    types.Map                      `tfsdk:"labels"`
	Report    types.String                   `tfsdk:"report"`
}

type EnvironmentTestResourceModel struct {
	Features types.List `tfsdk:"features"`
}

func (r *EnvResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_env"
}

func (r *EnvResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		// This description is used by the documentation generator and the language server.
		MarkdownDescription: "Example resource",

		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Example identifier",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"harness": schema.StringAttribute{
				Optional: true,
			},
			"report": schema.StringAttribute{
				MarkdownDescription: "The resulting environment test report.",
				Computed:            true,
			},
			"test": schema.ListAttribute{
				Optional: true,
				ElementType: basetypes.ObjectType{
					AttrTypes: map[string]attr.Type{
						"features": basetypes.ListType{
							ElemType: types.StringType,
						},
					},
				},
			},
			"labels": schema.MapAttribute{
				Optional:    true,
				ElementType: types.StringType,
			},
		},
	}
}

func (r *EnvResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	// Prevent panic if the provider has not been configured.
	if req.ProviderData == nil {
		return
	}

	store, ok := req.ProviderData.(*ProviderStore)
	if !ok {
		resp.Diagnostics.AddError("invalid provider data", "...")
		return
	}

	r.id = store.RandomID()
	r.store = store
}

func (r *EnvResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data EnvResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	data.Id = types.StringValue(r.id)

	// TODO: Define a real test report
	data.Report = types.StringValue(fmt.Sprintf(`{"id": "%s"}`, r.id))

	var harness itypes.Harness
	if data.HarnessId.IsNull() || data.HarnessId.IsUnknown() {
		// Create a new null harness
		harness = harnesses.NewBase()
	} else {
		h, ok := r.store.harnesses.Get(data.HarnessId.ValueString())
		if !ok {
			resp.Diagnostics.AddError("unknown harness id: "+data.HarnessId.ValueString(), "...")
			return
		}
		harness = h
	}

	// Continue only if labels provided are matched
	var elabels Labels
	if diags := data.Labels.ElementsAs(ctx, &elabels, false); diags.HasError() {
		diags.AddWarning("failed to convert labels", "...")
		resp.Diagnostics.Append(diags...)
	}

	if !r.store.labels.Match(elabels) {
		// we still need to mark the tests as triggered so teardown will not hang
		if _, err := harness.Finish()(ctx, nil); err != nil {
			resp.Diagnostics.AddError("failed to finish harness", err.Error())
			return
		}

		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		resp.Diagnostics.AddWarning("labels do not match", "skipping environment tests")
		return
	}

	// Register the harness with the environment
	env := envs.NewExec().
		Setup(harness.Setup()).
		Finish(harness.Finish())

	finish, err := env.Run(ctx)
	if err != nil {
		resp.Diagnostics.AddError("environment unexpectedly failed during setup", err.Error())
		return
	}
	defer finish()

	for _, test := range data.Tests {
		var featureIds []string
		if diags := test.Features.ElementsAs(ctx, &featureIds, false); diags.HasError() {
			resp.Diagnostics.Append(diags...)
			return
		}

		var feats []itypes.Feature
		for _, fid := range featureIds {
			f, ok := r.store.features.Get(fid)
			if !ok {
				resp.Diagnostics.AddError("no feature found", fmt.Sprintf("no feature foudn matching the id: %s", fid))
				return
			}

			builder := features.NewBuilder(f.Name.ValueString()).
				WithDescription(f.Description.ValueString())

			for _, setup := range f.Setups {
				builder = builder.WithAssertion(setup.WithLevel(itypes.Setup))
			}

			for _, teardown := range f.Teardowns {
				builder = builder.WithAssertion(teardown.WithLevel(itypes.Teardown))
			}

			for _, assert := range f.Assertions {
				builder = builder.WithAssertion(assert.WithLevel(itypes.Assessment))
			}

			feats = append(feats, builder.Build(env))
		}

		if err := env.Test(ctx, feats...); err != nil {
			resp.Diagnostics.AddError("environment unexpectedly failed while running test", err.Error())
			return
		}
	}

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *EnvResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data EnvResourceModel

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *EnvResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data EnvResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *EnvResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data EnvResourceModel

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}
}

func (r *EnvResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
