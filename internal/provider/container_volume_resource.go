package provider

import (
	"context"
	"fmt"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/containers/provider"
	"github.com/docker/docker/api/types/volume"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
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

	inv := InventoryDataSourceModel{}
	if diags := req.Config.GetAttribute(ctx, path.Root("inventory"), &inv); diags.HasError() {
		return
	}

	invEnc, err := r.store.Encode(inv.Seed.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("failed to create volume", "encoding inventory seed")
		return
	}

	id := fmt.Sprintf("%s-%s", data.Name.ValueString(), invEnc)
	_, err = r.store.cli.VolumeCreate(ctx, volume.CreateOptions{
		Name:   id,
		Labels: provider.DefaultLabels(),
	})
	if err != nil {
		resp.Diagnostics.AddError("failed to create volume", err.Error())
		return
	}

	data.Id = basetypes.NewStringValue(id)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ContainerVolumeResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
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
					"seed": schema.StringAttribute{
						Required: true,
					},
				},
			},
			"id": schema.StringAttribute{
				Description: "The unique identifier for this volume. This is generated from the volume name and inventory seed.",
				Computed:    true,
			},
		},
	}
}
