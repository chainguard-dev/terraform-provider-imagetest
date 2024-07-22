package provider

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harnesses/container"
	itypes "github.com/chainguard-dev/terraform-provider-imagetest/internal/types"
	"github.com/docker/docker/api/types/mount"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.ResourceWithModifyPlan = &HarnessContainerResource{}

func NewHarnessContainerResource() resource.Resource {
	return &HarnessContainerResource{}
}

// HarnessContainerResource defines the resource implementation.
type HarnessContainerResource struct {
	BaseHarnessResource
}

// HarnessContainerResourceModel describes the resource data model.
type HarnessContainerResourceModel struct {
	Id        types.String                     `tfsdk:"id"`
	Name      types.String                     `tfsdk:"name"`
	Inventory InventoryDataSourceModel         `tfsdk:"inventory"`
	Volumes   []FeatureHarnessVolumeMountModel `tfsdk:"volumes"`

	Image      types.String                             `tfsdk:"image"`
	Privileged types.Bool                               `tfsdk:"privileged"`
	Envs       types.Map                                `tfsdk:"envs"`
	Mounts     []ContainerResourceMountModel            `tfsdk:"mounts"`
	Networks   map[string]ContainerResourceModelNetwork `tfsdk:"networks"`
	Timeouts   timeouts.Value                           `tfsdk:"timeouts"`
}

type ContainerResourceMountModel struct {
	Source      types.String `tfsdk:"source"`
	Destination types.String `tfsdk:"destination"`
}

type ContainerResourceModelNetwork struct {
	Name types.String `tfsdk:"name"`
}

func (r *HarnessContainerResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_harness_container"
}

func (r *HarnessContainerResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data HarnessContainerResourceModel
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

func (r *HarnessContainerResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data HarnessContainerResourceModel
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

func (r *HarnessContainerResource) harness(ctx context.Context, data *HarnessContainerResourceModel) (itypes.Harness, diag.Diagnostics) {
	diags := make(diag.Diagnostics, 0)

	ref, err := name.ParseReference(data.Image.ValueString())
	if err != nil {
		return nil, []diag.Diagnostic{diag.NewErrorDiagnostic("invalid resource input", fmt.Sprintf("invalid image reference: %s", err))}
	}

	cfg := container.Config{
		Ref:        ref,
		Privileged: data.Privileged.ValueBool(),
		Mounts:     []container.ConfigMount{},
		Networks:   []string{},
		Env:        map[string]string{},
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
		if c := r.store.providerResourceData.Harnesses.Container; c != nil {
			mounts = append(mounts, c.Mounts...)

			for k, v := range c.Networks {
				networks[k] = v
			}

			envs := make(map[string]string)
			if diags := c.Envs.ElementsAs(ctx, &envs, false); diags.HasError() {
				return nil, diags
			}
			cfg.Env = envs
		}
	}

	for _, m := range mounts {
		src, err := filepath.Abs(m.Source.ValueString())
		if err != nil {
			return nil, []diag.Diagnostic{diag.NewErrorDiagnostic("invalid resource input", fmt.Sprintf("invalid mount source: %s", err))}
		}

		cfg.Mounts = append(cfg.Mounts, container.ConfigMount{
			Type:        mount.TypeBind,
			Source:      src,
			Destination: m.Destination.ValueString(),
		})
	}

	for _, network := range networks {
		cfg.Networks = append(cfg.Networks, network.Name.ValueString())
	}

	if data.Volumes != nil {
		for _, vol := range data.Volumes {
			cfg.ManagedVolumes = append(cfg.ManagedVolumes, container.ConfigMount{
				Type:        mount.TypeVolume,
				Source:      vol.Source.Id.ValueString(),
				Destination: vol.Destination,
			})
		}
	}

	envs := make(map[string]string)
	if diags := data.Envs.ElementsAs(ctx, &envs, false); diags.HasError() {
		return nil, diags
	}
	for k, v := range envs {
		cfg.Env[k] = v
	}

	harness := container.New(data.Id.ValueString(), r.store.cli, cfg)
	return harness, diags
}

func (r *HarnessContainerResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `A harness that runs steps in a sandbox container.`,
		Attributes: mergeResourceSchemas(
			r.schemaAttributes(ctx),
			defaultContainerResourceSchemaAttributes(),
			map[string]schema.Attribute{
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
			},
		),
	}
}

// defaultContainerResourceSchemaAttributes adds common container resource
// attributes to the given map. this function is provided knowing how common it
// is for other harnesses to require some sort of container configuration.
func defaultContainerResourceSchemaAttributes() map[string]schema.Attribute {
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
