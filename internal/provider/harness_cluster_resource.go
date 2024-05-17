package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure provider defined types fully satisfy framework interfaces.
var (
	_ resource.Resource                = &HarnessClusterResource{}
	_ resource.ResourceWithConfigure   = &HarnessClusterResource{}
	_ resource.ResourceWithImportState = &HarnessClusterResource{}
	_ resource.ResourceWithModifyPlan  = &HarnessClusterResource{}
)

const (
	ClusterTypeGKE = "GKE"
)

type HarnessClusterResource struct {
	Id        types.String             `tfsdk:"id"`
	Name      types.String             `tfsdk:"name"`
	Inventory InventoryDataSourceModel `tfsdk:"inventory"`
	Skipped   types.Bool               `tfsdk:"skipped"`
	Timeouts  timeouts.Value           `tfsdk:"timeouts"`

	Type types.String `tfsdk:"type"`
}

func (h *HarnessClusterResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	//TODO implement me
	panic("implement me")
}

func (h *HarnessClusterResource) Configure(ctx context.Context, request resource.ConfigureRequest, response *resource.ConfigureResponse) {
	//TODO implement me
	panic("implement me")
}

func (h *HarnessClusterResource) Metadata(ctx context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	//TODO implement me
	panic("implement me")
}

func (h *HarnessClusterResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	//TODO implement me
	panic("implement me")
}

func (h *HarnessClusterResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	//TODO implement me
	panic("implement me")
}

func (h *HarnessClusterResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	//TODO implement me
	panic("implement me")
}

func (h *HarnessClusterResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	//TODO implement me
	panic("implement me")
}

func (h *HarnessClusterResource) ModifyPlan(ctx context.Context, request resource.ModifyPlanRequest, response *resource.ModifyPlanResponse) {
	//TODO implement me
	panic("implement me")
}

func (h *HarnessClusterResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `A harness that provisions a remote cluster in a cloud environment and runs steps as requested`,
		Attributes:          addHarnessResourceSchemaAttributes(ctx),
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
