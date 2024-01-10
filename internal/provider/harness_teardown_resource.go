package provider

import (
	"context"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/log"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure provider defined types fully satisfy framework interfaces.
var (
	_ resource.Resource                = &HarnessTeardownResource{}
	_ resource.ResourceWithConfigure   = &HarnessTeardownResource{}
	_ resource.ResourceWithImportState = &HarnessTeardownResource{}
)

func NewHarnessTeardownResource() resource.Resource {
	return &HarnessTeardownResource{}
}

// HarnessTeardownResource defines the resource implementation.
type HarnessTeardownResource struct {
	store *ProviderStore
}

// HarnessTeardownResourceModel describes the resource data model.
type HarnessTeardownResourceModel struct {
	HarnessId types.String `tfsdk:"harness"`
}

func (r *HarnessTeardownResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_harness_teardown"
}

func (r *HarnessTeardownResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `A teardown signal used to destroy the provided harness after all referenced Features have completed.`,

		Attributes: map[string]schema.Attribute{
			"harness": schema.StringAttribute{
				Description: "The id of the harness to teardown.",
				Required:    true,
			},
		},
	}
}

func (r *HarnessTeardownResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *HarnessTeardownResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data HarnessTeardownResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	ctx = log.WithCtx(ctx, r.store.Logger())

	harness, ok := r.store.harnesses.Get(data.HarnessId.ValueString())
	if !ok {
		resp.Diagnostics.AddError("invalid harness id", "...")
		return
	}

	log.Info(ctx, "waiting for features referencing harness to complete", "harness", data.HarnessId.ValueString())
	if err := harness.Done(); err != nil {
		resp.Diagnostics.AddError("harness failed", err.Error())
		return
	}

	log.Info(ctx, "features references complete, destroying harness")
	if err := harness.Destroy(ctx); err != nil {
		resp.Diagnostics.AddError("failed during harness destroy", err.Error())
		return
	}

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *HarnessTeardownResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data HarnessTeardownResourceModel

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *HarnessTeardownResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data HarnessTeardownResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *HarnessTeardownResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data HarnessTeardownResourceModel

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}
}

func (r *HarnessTeardownResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
