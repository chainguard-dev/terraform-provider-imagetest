package provider

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harnesses"
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
)

func NewHarnessContainerResource() resource.Resource {
	return &HarnessContainerResource{}
}

// HarnessContainerResource defines the resource implementation.
type HarnessContainerResource struct {
	id    string
	store *ProviderStore
}

// HarnessContainerResourceModel describes the resource data model.
type HarnessContainerResourceModel struct {
	Id         types.String                         `tfsdk:"id"`
	Image      types.String                         `tfsdk:"image"`
	Privileged types.Bool                           `tfsdk:"privileged"`
	Envs       types.Map                            `tfsdk:"envs"`
	Mounts     []HarnessContainerResourceMountModel `tfsdk:"mounts"`
}

type HarnessContainerResourceMountModel struct {
	Source      types.String `tfsdk:"source"`
	Destination types.String `tfsdk:"destination"`
}

func (r *HarnessContainerResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_harness_container"
}

func (r *HarnessContainerResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `A harness that runs steps in a sandbox container.`,

		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true,
			},
			"image": schema.StringAttribute{
				Description: "The full image reference to use for the k3s container.",
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
							Description: "The absolute path on the container to mount the source directory to.",
							Required:    true,
						},
					},
				},
			},
		},
	}
}

func (r *HarnessContainerResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *HarnessContainerResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data HarnessContainerResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	data.Id = types.StringValue(r.id)

	cfg, err := r.containerCfg(ctx, data)
	if err != nil {
		resp.Diagnostics.AddError("invalid resource data", err.Error())
		return
	}

	harness, err := harnesses.NewContainer(ctx, r.id, cfg)
	if err != nil {
		resp.Diagnostics.AddError("invalid provider data", "...")
		return
	}
	r.store.harnesses.Set(r.id, harness)

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *HarnessContainerResource) containerCfg(ctx context.Context, data HarnessContainerResourceModel) (harnesses.ContainerConfig, error) {
	cfg := harnesses.ContainerConfig{
		Image:      data.Image.ValueString(),
		Privileged: data.Privileged.ValueBool(),
	}

	mounts := []harnesses.ContainerConfigMount{}
	for _, mount := range data.Mounts {
		src, err := filepath.Abs(mount.Source.ValueString())
		if err != nil {
			return harnesses.ContainerConfig{}, err
		}

		mounts = append(mounts, harnesses.ContainerConfigMount{
			Source:      src,
			Destination: mount.Destination.ValueString(),
		})
	}
	cfg.Mounts = mounts

	envs := map[string]string{}
	if diags := data.Envs.ElementsAs(ctx, &envs, false); diags.HasError() {
		return harnesses.ContainerConfig{}, fmt.Errorf("invalid envs input: %w", diags.Errors())
	}
	cfg.Env = envs

	return cfg, nil
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
