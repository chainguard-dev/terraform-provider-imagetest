package provider

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harnesses/container"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/log"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure provider defined types fully satisfy framework interfaces.
var (
	_ resource.Resource                = &HarnessContainerResource{}
	_ resource.ResourceWithConfigure   = &HarnessContainerResource{}
	_ resource.ResourceWithImportState = &HarnessContainerResource{}
	_ resource.ResourceWithModifyPlan  = &HarnessContainerResource{}
)

func NewHarnessContainerResource() resource.Resource {
	return &HarnessContainerResource{}
}

// HarnessContainerResource defines the resource implementation.
type HarnessContainerResource struct {
	HarnessResource
}

// HarnessContainerResourceModel describes the resource data model.
type HarnessContainerResourceModel struct {
	Id        types.String             `tfsdk:"id"`
	Name      types.String             `tfsdk:"name"`
	Inventory InventoryDataSourceModel `tfsdk:"inventory"`
	Skipped   types.Bool               `tfsdk:"skipped"`

	Image      types.String                             `tfsdk:"image"`
	Privileged types.Bool                               `tfsdk:"privileged"`
	Envs       types.Map                                `tfsdk:"envs"`
	Mounts     []ContainerResourceMountModel            `tfsdk:"mounts"`
	Networks   map[string]ContainerResourceModelNetwork `tfsdk:"networks"`
}

type ContainerResourceMountModel struct {
	Source      types.String `tfsdk:"source"`
	Destination types.String `tfsdk:"destination"`
}

type ContainerResourceModelNetwork struct {
	Name types.String `tfsdk:"name"`
}

func (r *HarnessContainerResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_harness_container"
}

func (r *HarnessContainerResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `A harness that runs steps in a sandbox container.`,

		Attributes: addHarnessResourceSchemaAttributes(
			addContainerResourceSchemaAttributes(),
		),
	}
}

func (r *HarnessContainerResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	ctx = log.WithCtx(ctx, r.store.Logger())

	var data HarnessContainerResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	skip := r.ShouldSkip(ctx, req, resp)
	if resp.Diagnostics.HasError() {
		return
	}
	data.Skipped = types.BoolValue(skip)

	if data.Skipped.ValueBool() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		return
	}

	cfg := container.Config{
		Image:      data.Image.ValueString(),
		Privileged: data.Privileged.ValueBool(),
		Mounts:     []container.ConfigMount{},
		Networks:   []string{},
		Env:        map[string]string{},
	}

	mounts := []ContainerResourceMountModel{}
	if data.Mounts != nil {
		mounts = data.Mounts
	}

	networks := make(map[string]ContainerResourceModelNetwork)
	if data.Networks != nil {
		networks = data.Networks
	}

	if r.store.providerResourceData.Harnesses != nil {
		if c := r.store.providerResourceData.Harnesses.Container; c != nil {
			mounts = append(mounts, c.Mounts...)

			for k, v := range c.Networks {
				networks[k] = v
			}

			envs := make(map[string]string)
			if diags := c.Envs.ElementsAs(ctx, &envs, false); diags.HasError() {
				resp.Diagnostics.AddError("invalid resource input", fmt.Sprintf("invalid envs input: %s", diags.Errors()))
				return
			}
			cfg.Env = envs
		}
	}

	for _, mount := range mounts {
		src, err := filepath.Abs(mount.Source.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("invalid resource input", fmt.Sprintf("invalid mount source: %s", err))
			return
		}

		cfg.Mounts = append(cfg.Mounts, container.ConfigMount{
			Source:      src,
			Destination: mount.Destination.ValueString(),
		})
	}

	for _, network := range networks {
		cfg.Networks = append(cfg.Networks, network.Name.ValueString())
	}

	envs := make(map[string]string)
	if diags := data.Envs.ElementsAs(ctx, &envs, false); diags.HasError() {
		resp.Diagnostics.AddError("invalid resource input", fmt.Sprintf("invalid envs input: %s", diags.Errors()))
		return
	}
	for k, v := range envs {
		cfg.Env[k] = v
	}

	harness, err := container.New(ctx, data.Id.ValueString(), cfg)
	if err != nil {
		resp.Diagnostics.AddError("invalid provider data", "...")
		return
	}
	r.store.harnesses.Set(data.Id.ValueString(), harness)

	log.Info(ctx, fmt.Sprintf("creating container harness [%s]", data.Id.ValueString()))

	// Finally, create the harness
	// TODO: Change this signature
	if _, err := harness.Setup()(ctx); err != nil {
		resp.Diagnostics.AddError("failed to setup harness", err.Error())
		return
	}

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *HarnessContainerResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data HarnessContainerResourceModel

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *HarnessContainerResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data HarnessContainerResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *HarnessContainerResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data HarnessContainerResourceModel

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}
}

func (r *HarnessContainerResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// addContainerResourceSchemaAttributes adds common container resource
// attributes to the given map. this function is provided knowing how common it
// is for other harnesses to require some sort of container configuration.
func addContainerResourceSchemaAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"image": schema.StringAttribute{
			Description: "The full image reference to use for the container.",
			Optional:    true,
			Computed:    true,
			Default:     stringdefault.StaticString("cgr.dev/chainguard/wolfi-base:latest"),
		},
		"privileged": schema.BoolAttribute{
			Optional: true,
			Computed: true,
			Default:  booldefault.StaticBool(false),
		},
		"envs": schema.MapAttribute{
			Description: "Environment variables to set on the container.",
			Optional:    true,
			ElementType: types.StringType,
		},
		"networks": schema.MapNestedAttribute{
			Description: "A map of existing networks to attach the container to.",
			Optional:    true,
			NestedObject: schema.NestedAttributeObject{
				Attributes: map[string]schema.Attribute{
					"name": schema.StringAttribute{
						Description: "The name of the existing network to attach the container to.",
						Required:    true,
					},
				},
			},
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
				},
			},
		},
	}
}
