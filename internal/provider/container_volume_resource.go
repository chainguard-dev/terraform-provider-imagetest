package provider

import (
	"context"
	"fmt"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/log"
	"github.com/docker/docker/api/types/volume"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

var (
	_ resource.Resource                = &ContainerVolumeResource{}
	_ resource.ResourceWithConfigure   = &ContainerVolumeResource{}
	_ resource.ResourceWithImportState = &ContainerVolumeResource{}
)

const metadataSuffix = "_container_volume"

type ContainerVolumeResource struct {
	store *ProviderStore
}

type ContainerVolumeResourceModel struct {
	Id        types.String             `tfsdk:"id"`
	Name      types.String             `tfsdk:"name"`
	Inventory InventoryDataSourceModel `tfsdk:"inventory"`
}

func NewContainerVolumeResource() resource.Resource {
	return &ContainerVolumeResource{}
}

func (r *ContainerVolumeResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	store, ok := req.ProviderData.(*ProviderStore)
	if !ok {
		resp.Diagnostics.AddError("invalid provider data", "unable to convert provider data to the correct type")
		return
	}

	r.store = store
}

func (r *ContainerVolumeResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + metadataSuffix
}

func (r *ContainerVolumeResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `A volume in the container engine that can be referenced by containers.`,
		Attributes:          ContainerVolumeResourceAttributes(),
	}
}

func ContainerVolumeResourceAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
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
	}
}

func (r *ContainerVolumeResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	ctx = log.WithCtx(ctx, r.store.Logger())

	var data ContainerVolumeResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	inv := InventoryDataSourceModel{}
	if diags := req.Config.GetAttribute(ctx, path.Root("inventory"), &inv); diags.HasError() {
		resp.Diagnostics.AddError("failed to create volume", "retrieving inventory")
		return
	}

	invEnc, err := r.store.Encode(inv.Seed.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("failed to create volume", "encoding inventory seed")
		return
	}

	id := fmt.Sprintf("%s-%s", data.Name.ValueString(), invEnc)
	_, err = r.store.cli.VolumeCreate(ctx, volume.CreateOptions{
		Name: id,
	})
	if err != nil {
		resp.Diagnostics.AddError("failed to create volume", err.Error())
		return
	}

	data.Id = basetypes.NewStringValue(id)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ContainerVolumeResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data ContainerVolumeResourceModel

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ContainerVolumeResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data ContainerVolumeResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ContainerVolumeResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data ContainerVolumeResourceModel

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}
}

func (r *ContainerVolumeResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
