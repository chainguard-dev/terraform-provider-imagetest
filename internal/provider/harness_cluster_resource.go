package provider

import (
	"context"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/util"
	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const (
	ClusterTypeGKE = "GKE"
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

	Type types.String `tfsdk:"type"`
}

func (h *HarnessClusterResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	//TODO implement me
	panic("implement me")
}

func (h *HarnessClusterResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data HarnessClusterResourceModel
	baseRead(ctx, &data, req, resp)
}

func (h *HarnessClusterResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data HarnessClusterResourceModel
	baseUpdate(ctx, &data, req, resp)
}

func (h *HarnessClusterResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data HarnessClusterResourceModel
	baseDelete(ctx, &data, req, resp)
}

func (h *HarnessClusterResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (h *HarnessClusterResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_harness_cluster"
}

func (h *HarnessClusterResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `A harness that provisions a remote cluster in a cloud environment and runs steps as requested`,
		Attributes: util.MergeSchemaMaps(
			addHarnessResourceSchemaAttributes(ctx),
			clusterHarnessSchemaAttributes(),
		),
	}
}

func clusterHarnessSchemaAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"provider": schema.StringAttribute{
			Required:    true,
			Description: "Type of the remote cluster to be created",
			Validators: []validator.String{
				stringvalidator.OneOf(ClusterTypeGKE),
			},
		},
	}
}
