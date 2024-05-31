package provider

import (
	"context"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harnesses/remote"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/util"
	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure provider-defined types fully satisfy framework interfaces.
var (
	_ resource.Resource                = &HarnessClusterResource{}
	_ resource.ResourceWithConfigure   = &HarnessClusterResource{}
	_ resource.ResourceWithImportState = &HarnessClusterResource{}
	_ resource.ResourceWithModifyPlan  = &HarnessClusterResource{}
)

func NewHarnessClusterResource() resource.Resource {
	return &HarnessClusterResource{}
}

type HarnessClusterResource struct {
	HarnessResource
}

type HarnessClusterResourceModel struct {
	Id        types.String             `tfsdk:"id"`
	Name      types.String             `tfsdk:"name"`
	Inventory InventoryDataSourceModel `tfsdk:"inventory"`
	Skipped   types.Bool               `tfsdk:"skipped"`
	Timeouts  timeouts.Value           `tfsdk:"timeouts"`
}

func (r *HarnessClusterResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data HarnessClusterResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	skipped := r.ShouldSkip(ctx, req, resp)
	if resp.Diagnostics.HasError() {
		return
	}
	data.Skipped = types.BoolValue(skipped)

	if data.Skipped.ValueBool() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		return
	}

	timeout, diags := data.Timeouts.Create(ctx, defaultHarnessCreateTimeout)
	resp.Diagnostics.Append(diags...)

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ctx, err := r.store.Logger(ctx, data.Inventory, "harness_id", data.Id.ValueString(), "harness_name", data.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("failed to initialize logger(s)", err.Error())
		return
	}

	var kubeconfig *string
	if r.store.providerResourceData.Harnesses != nil {
		if pc := r.store.providerResourceData.Harnesses.Cluster; pc != nil {
			kubeconfig = pc.Kubeconfig
		}
	}

	id := data.Id.ValueString()
	harness, err := remote.New(id, kubeconfig)
	if err != nil {
		resp.Diagnostics.AddError("failed to create cluster harness", err.Error())
		return
	}
	r.store.harnesses.Set(id, harness)

	// this is a no-op for the cluster harness at this time
	// leaving the call here for future-proofing
	if _, err := harness.Setup()(ctx); err != nil {
		resp.Diagnostics.AddError("failed to setup harness", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *HarnessClusterResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data HarnessClusterResourceModel
	baseRead(ctx, &data, req, resp)
}

func (r *HarnessClusterResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data HarnessClusterResourceModel
	baseUpdate(ctx, &data, req, resp)
}

func (r *HarnessClusterResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data HarnessClusterResourceModel
	baseDelete(ctx, &data, req, resp)
}

func (r *HarnessClusterResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (r *HarnessClusterResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_harness_cluster"
}

func (r *HarnessClusterResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `A harness that provisions a remote cluster in a cloud environment and runs steps as requested`,
		Attributes: util.MergeSchemaMaps(
			addHarnessResourceSchemaAttributes(ctx),
		),
	}
}
