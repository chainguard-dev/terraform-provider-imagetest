package provider

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/containers/provider"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harnesses/container"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harnesses/docker"
	"github.com/docker/docker/api/types/mount"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/log"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const (
	ContainerImage = "cgr.dev/chainguard/wolfi-base:latest" // TODO(mauren): create a docker-cli image
)

// Ensure provider defined types fully satisfy framework interfaces.
var (
	_ resource.Resource                = &HarnessDockerResource{}
	_ resource.ResourceWithConfigure   = &HarnessDockerResource{}
	_ resource.ResourceWithImportState = &HarnessDockerResource{}
	_ resource.ResourceWithModifyPlan  = &HarnessDockerResource{}
)

func NewHarnessDockerResource() resource.Resource {
	return &HarnessDockerResource{}
}

// HarnessDockerResource defines the resource implementation.
type HarnessDockerResource struct {
	HarnessResource
}

// HarnessDockerResourceModel describes the resource data model.
type HarnessDockerResourceModel struct {
	Id        types.String                     `tfsdk:"id"`
	Name      types.String                     `tfsdk:"name"`
	Inventory InventoryDataSourceModel         `tfsdk:"inventory"`
	Skipped   types.Bool                       `tfsdk:"skipped"`
	Volumes   []FeatureHarnessVolumeMountModel `tfsdk:"volumes"`

	Image      types.String                             `tfsdk:"image"`
	Privileged types.Bool                               `tfsdk:"privileged"`
	Envs       types.Map                                `tfsdk:"envs"`
	Mounts     []ContainerResourceMountModel            `tfsdk:"mounts"`
	Networks   map[string]ContainerResourceModelNetwork `tfsdk:"networks"`
}

func (r *HarnessDockerResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_harness_docker"
}

func (r *HarnessDockerResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `A harness that runs steps in a sandbox container with access to a Docker daemon.`,

		Attributes: addHarnessResourceSchemaAttributes(
			addDockerResourceSchemaAttributes(map[string]schema.Attribute{}),
		),
	}
}

func (r *HarnessDockerResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data HarnessDockerResourceModel
	var opts []docker.Option

	ctx = log.WithCtx(ctx, r.store.Logger())

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

	ref, err := name.ParseReference(data.Image.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("invalid resource input", fmt.Sprintf("invalid image reference: %s", err))
		return
	}
	opts = append(opts, docker.WithImageRef(ref))

	if r.store.providerResourceData.Harnesses != nil &&
		r.store.providerResourceData.Harnesses.Docker != nil &&
		r.store.providerResourceData.Harnesses.Docker.SocketPath != nil {
		opts = append(opts, docker.WithSocketPath(*r.store.providerResourceData.Harnesses.Docker.SocketPath))
	}

	mounts := make([]ContainerResourceMountModel, 0)
	if data.Mounts != nil {
		mounts = data.Mounts
	}

	networks := make(map[string]ContainerResourceModelNetwork)
	if data.Networks != nil {
		networks = data.Networks
	}

	if r.store.providerResourceData.Harnesses != nil {
		if c := r.store.providerResourceData.Harnesses.Docker; c != nil {
			mounts = append(mounts, c.Mounts...)

			for k, v := range c.Networks {
				networks[k] = v
			}

			envs := make(provider.Env)
			if diags := c.Envs.ElementsAs(ctx, &envs, false); diags.HasError() {
				return
			}
			opts = append(opts, docker.WithEnvs(envs))
		}
	}

	for _, m := range mounts {
		src, err := filepath.Abs(m.Source.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("invalid resource input", fmt.Sprintf("invalid mount source: %s", err))
			return
		}

		opts = append(opts, docker.WithMounts(container.ConfigMount{
			Type:        mount.TypeBind,
			Source:      src,
			Destination: m.Destination.ValueString(),
		}))
	}

	for _, network := range networks {
		opts = append(opts, docker.WithNetworks(network.Name.ValueString()))
	}

	if data.Volumes != nil {
		for _, vol := range data.Volumes {
			opts = append(opts, docker.WithManagedVolumes(container.ConfigMount{
				Type:        mount.TypeVolume,
				Source:      vol.Source.Id.ValueString(),
				Destination: vol.Destination,
			}))
		}
	}

	envs := make(provider.Env)
	if diags := data.Envs.ElementsAs(ctx, &envs, false); diags.HasError() {
		return
	}
	opts = append(opts, docker.WithEnvs(envs))

	id := data.Id.ValueString()

	harness, err := docker.New(id, r.store.cli, opts...)
	if err != nil {
		resp.Diagnostics.AddError("invalid provider data", err.Error())
		return
	}
	r.store.harnesses.Set(id, harness)

	log.Info(ctx, fmt.Sprintf("creating container harness [%s]", id))

	// Finally, create the harness
	// TODO: Change this signature
	if _, err := harness.Setup()(ctx); err != nil {
		resp.Diagnostics.AddError("failed to setup harness", err.Error())
		return
	}

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *HarnessDockerResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data HarnessDockerResourceModel

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *HarnessDockerResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data HarnessDockerResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *HarnessDockerResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data HarnessDockerResourceModel

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}
}

func (r *HarnessDockerResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func addDockerResourceSchemaAttributes(attrs map[string]schema.Attribute) map[string]schema.Attribute {
	defaults := map[string]schema.Attribute{
		"image": schema.StringAttribute{
			Description: "The full image reference to use for the container.",
			Optional:    true,
			Computed:    true,
			Default:     stringdefault.StaticString(ContainerImage),
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
		"volumes": schema.ListNestedAttribute{
			NestedObject: schema.NestedAttributeObject{
				Attributes: map[string]schema.Attribute{
					"source": schema.SingleNestedAttribute{
						Attributes: map[string]schema.Attribute{
							"id": schema.StringAttribute{
								Required: true,
							},
							"name": schema.StringAttribute{
								Required: true,
							},
							"inventory": schema.SingleNestedAttribute{
								Required: true,
								Attributes: map[string]schema.Attribute{
									"seed": schema.StringAttribute{
										Required: true,
									},
								},
							},
						},
						Required: true,
					},
					"destination": schema.StringAttribute{
						Required: true,
					},
				},
			},
			Description: "The volumes this harness should mount. This is received as a mapping from imagetest_container_volume resources to destination folders.",
			Optional:    true,
		},
	}

	for k, v := range attrs {
		defaults[k] = v
	}

	return defaults
}
