package provider

import (
	"context"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness/volume"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.ResourceWithModifyPlan = &ContainerVolumeResource{}

const metadataSuffix = "_container_volume"

type ContainerVolumeResource struct {
	BaseHarnessResource
}

type ContainerVolumeResourceModel struct {
	Id        types.String             `tfsdk:"id"`
	Name      types.String             `tfsdk:"name"`
	Inventory InventoryDataSourceModel `tfsdk:"inventory"`
}

func NewContainerVolumeResource() resource.Resource {
	return &ContainerVolumeResource{}
}

func (r *ContainerVolumeResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + metadataSuffix
}

func (r *ContainerVolumeResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data ContainerVolumeResourceModel
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

func (r *ContainerVolumeResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data ContainerVolumeResourceModel
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

func (r *ContainerVolumeResource) harness(_ context.Context, data *ContainerVolumeResourceModel) (harness.Harness, diag.Diagnostics) {
	diags := make(diag.Diagnostics, 0)

	id := data.Id.ValueString()
	harness := volume.New(volume.WithName(id))

	return harness, diags
}

func (r *ContainerVolumeResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `A volume in the container engine that can be referenced by containers.`,
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Description: "A name for this volume resource.",
				Required:    true,
			},
			"inventory": schema.SingleNestedAttribute{
				Description: "The inventory this volume belongs to. This is received as a direct input from a data.imagetest_inventory data source.",
				Required:    true,
				Attributes: map[string]schema.Attribute{
					"id": schema.StringAttribute{
						Required: true,
					},
				},
			},
			"id": schema.StringAttribute{
				Description: "The unique identifier for this volume. This is generated from the volume name and inventory id.",
				Computed:    true,
			},
		},
	}
}
