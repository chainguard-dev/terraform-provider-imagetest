package provider

import (
	"context"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness/remote"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/util"
	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.ResourceWithModifyPlan = &HarnessClusterResource{}

func NewHarnessClusterResource() resource.Resource {
	return &HarnessClusterResource{}
}

type HarnessClusterResource struct {
	BaseHarnessResource
}

type HarnessClusterResourceModel struct {
	Id        types.String             `tfsdk:"id"`
	Name      types.String             `tfsdk:"name"`
	Inventory InventoryDataSourceModel `tfsdk:"inventory"`
	Skipped   types.Bool               `tfsdk:"skipped"`
	Timeouts  timeouts.Value           `tfsdk:"timeouts"`
}

func (r *HarnessClusterResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_harness_cluster"
}

func (r *HarnessClusterResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data HarnessClusterResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)

	harness, diags := r.harness(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if diags.HasError() {
		return
	}

	resp.Diagnostics.Append(r.create(ctx, req, harness)...)
	if resp.Diagnostics.HasError() {
		return
	}
}

func (r *HarnessClusterResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data HarnessClusterResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)

	harness, diags := r.harness(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if diags.HasError() {
		return
	}

	resp.Diagnostics.Append(r.update(ctx, req, harness)...)
	if resp.Diagnostics.HasError() {
		return
	}
}

func (r *HarnessClusterResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
}

func (r *HarnessClusterResource) harness(_ context.Context, data *HarnessClusterResourceModel) (harness.Harness, diag.Diagnostics) {
	diags := make(diag.Diagnostics, 0)

	var kubeconfig *string
	if r.store.providerResourceData.Harnesses != nil {
		if pc := r.store.providerResourceData.Harnesses.Cluster; pc != nil {
			kubeconfig = pc.Kubeconfig
		}
	}

	id := data.Id.ValueString()
	harness, err := remote.New(id, kubeconfig)
	if err != nil {
		return nil, []diag.Diagnostic{diag.NewErrorDiagnostic("failed to create cluster harness", err.Error())}
	}

	return harness, diags
}

func (r *HarnessClusterResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `A harness that provisions a remote cluster in a cloud environment and runs steps as requested`,
		Attributes: util.MergeSchemaMaps(
			r.schemaAttributes(ctx),
		),
	}
}
