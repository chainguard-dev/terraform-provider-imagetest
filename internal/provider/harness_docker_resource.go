package provider

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/bundler"
	client "github.com/chainguard-dev/terraform-provider-imagetest/internal/docker"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness/docker"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/log"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/provider/framework"
	"github.com/docker/docker/api/types/mount"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const (
	ContainerImage = "cgr.dev/chainguard/docker-cli:latest-dev"
)

var _ resource.ResourceWithModifyPlan = &HarnessDockerResource{}

func NewHarnessDockerResource() resource.Resource {
	return &HarnessDockerResource{WithTypeName: "harness_docker"}
}

// HarnessDockerResource defines the resource implementation.
type HarnessDockerResource struct {
	framework.WithTypeName
	BaseHarnessResource
}

// HarnessDockerResourceModel describes the resource data model.
type HarnessDockerResourceModel struct {
	BaseHarnessResourceModel

	Image        types.String                           `tfsdk:"image"`
	Volumes      []FeatureHarnessVolumeMountModel       `tfsdk:"volumes"`
	Privileged   types.Bool                             `tfsdk:"privileged"`
	Envs         *HarnessContainerEnvs                  `tfsdk:"envs"`
	Mounts       []ContainerMountModel                  `tfsdk:"mounts"`
	Layers       []ContainerMountModel                  `tfsdk:"layers"`
	Packages     []string                               `tfsdk:"packages"`
	Repositories []string                               `tfsdk:"repositories"`
	Keyrings     []string                               `tfsdk:"keyrings"`
	Networks     map[string]ContainerNetworkModel       `tfsdk:"networks"`
	Registries   map[string]DockerRegistryResourceModel `tfsdk:"registries"`
	Resources    *ContainerResources                    `tfsdk:"resources"`
}

type DockerRegistryResourceModel struct {
	Auth *RegistryResourceAuthModel `tfsdk:"auth"`
}

func (r *HarnessDockerResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data HarnessDockerResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	log.Info(ctx, "creating docker harness", "id", data.Id.ValueString())

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

func (r *HarnessDockerResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data HarnessDockerResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	log.Info(ctx, "updating docker harness", "id", data.Id.ValueString())

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

func (r *HarnessDockerResource) harness(ctx context.Context, data *HarnessDockerResourceModel) (harness.Harness, diag.Diagnostics) {
	diags := make(diag.Diagnostics, 0)

	opts := []docker.Option{
		docker.WithName(data.Id.ValueString()),
	}

	mounts := make([]ContainerMountModel, 0)
	if data.Mounts != nil {
		mounts = data.Mounts
	}

	registries := make(map[string]DockerRegistryResourceModel)
	if data.Registries != nil {
		registries = data.Registries
	}

	networks := make(map[string]client.NetworkAttachment)
	if data.Networks != nil {
		networks = make(map[string]client.NetworkAttachment)
		for k, v := range data.Networks {
			networks[k] = client.NetworkAttachment{
				ID: v.Name.ValueString(),
			}
		}
	}

	volumes := make([]docker.VolumeConfig, 0)
	if data.Volumes != nil {
		for _, vol := range data.Volumes {
			volumes = append(volumes, docker.VolumeConfig{
				Name:   vol.Source.Id.ValueString(),
				Target: vol.Destination,
			})
		}
	}
	opts = append(opts, docker.WithVolumes(volumes...))

	if res := data.Resources; res != nil {
		resources, err := ParseResources(res)
		if err != nil {
			return nil, []diag.Diagnostic{diag.NewErrorDiagnostic("failed to parse resources", err.Error())}
		}
		log.Info(ctx, "Setting resources for docker harness", "cpu_limit", resources.CpuLimit.String(), "cpu_request", resources.CpuRequest.String(), "memory_limit", resources.MemoryLimit.String(), "memory_request", resources.MemoryRequest.String())
		opts = append(opts, docker.WithResources(client.ResourcesRequest{
			MemoryRequest: resources.MemoryRequest,
			MemoryLimit:   resources.MemoryLimit,
			CpuRequest:    resources.CpuRequest,
		}))
	}

	if r.store.providerResourceData.Harnesses != nil {
		if c := r.store.providerResourceData.Harnesses.Docker; c != nil {
			mounts = append(mounts, c.Mounts...)

			for k, v := range c.Networks {
				networks[k] = client.NetworkAttachment{
					ID: v.Name.ValueString(),
				}
			}

			for k, v := range c.Registries {
				registries[k] = v
			}

			if c.Envs != nil {
				opts = append(opts, docker.WithEnvs(c.Envs.Slice()...))
			}
		}
	}

	if data.Envs != nil {
		opts = append(opts, docker.WithEnvs(data.Envs.Slice()...))
	}

	for regAddress, regInfo := range registries {
		if regInfo.Auth != nil {
			if regInfo.Auth.Auth.IsNull() && regInfo.Auth.Password.IsNull() && regInfo.Auth.Username.IsNull() {
				opts = append(opts, docker.WithAuthFromKeychain(regAddress))
			} else {
				opts = append(opts,
					docker.WithAuthFromStatic(
						regAddress,
						regInfo.Auth.Username.ValueString(),
						regInfo.Auth.Password.ValueString(),
						regInfo.Auth.Auth.ValueString()))
			}
		}
	}

	b, err := r.bundler(data)
	if err != nil {
		return nil, []diag.Diagnostic{diag.NewErrorDiagnostic("failed to create bundler", err.Error())}
	}

	var layers []bundler.Layerer
	for _, sl := range data.Layers {
		layers = append(layers, bundler.NewFSLayer(
			os.DirFS(sl.Source.ValueString()),
			sl.Destination.ValueString(),
		))
	}

	bref, err := b.Bundle(ctx, r.store.repo, layers...)
	if err != nil {
		return nil, []diag.Diagnostic{diag.NewErrorDiagnostic("failed to bundle image", err.Error())}
	}
	opts = append(opts, docker.WithImageRef(bref))

	for _, m := range mounts {
		src, err := filepath.Abs(m.Source.ValueString())
		if err != nil {
			return nil, []diag.Diagnostic{diag.NewErrorDiagnostic("invalid resource input", fmt.Sprintf("invalid mount source: %s", err))}
		}

		opts = append(opts, docker.WithMounts(mount.Mount{
			Type:   mount.TypeBind,
			Source: src,
			Target: m.Destination.ValueString(),
		}))
	}

	for _, network := range networks {
		opts = append(opts, docker.WithNetworks(network))
	}

	harness, err := docker.New(opts...)
	if err != nil {
		return nil, []diag.Diagnostic{diag.NewErrorDiagnostic("invalid provider data", err.Error())}
	}

	return harness, diags
}

func (r *HarnessDockerResource) bundler(data *HarnessDockerResourceModel) (bundler.Bundler, error) {
	if data.Image.ValueString() != "" {
		ref, err := name.ParseReference(data.Image.ValueString())
		if err != nil {
			return nil, fmt.Errorf("invalid reference: %w", err)
		}

		return bundler.NewAppender(ref,
			bundler.AppenderWithRemoteOptions(r.store.ropts...),
		)
	}

	// for everything else, use some variation of the apko bundler
	opts := []bundler.ApkoOpt{
		bundler.ApkoWithPackages("docker", "docker-dind", "dockerd-oci-entrypoint"),
		bundler.ApkoWithRemoteOptions(r.store.ropts...),
		bundler.ApkoWithPackages(data.Packages...),
		bundler.ApkoWithRepositories(data.Repositories...),
		bundler.ApkoWithKeyrings(data.Keyrings...),
	}

	if p := r.store.providerResourceData.Sandbox; p != nil {
		opts = append(opts,
			bundler.ApkoWithPackages(p.ExtraPackages...),
			bundler.ApkoWithRepositories(p.ExtraRepos...),
			bundler.ApkoWithKeyrings(p.ExtraKeyrings...),
		)
	}

	return bundler.NewApko(opts...)
}

func (r *HarnessDockerResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `A harness that runs steps in a sandbox container with access to a Docker daemon.`,
		Attributes: mergeResourceSchemas(
			r.schemaAttributes(ctx),
			map[string]schema.Attribute{
				"image": schema.StringAttribute{
					Description: "The full image reference to use for the container.",
					Optional:    true,
				},
				"packages": schema.ListAttribute{
					Description: "A list of packages to install in the container.",
					Optional:    true,
					ElementType: types.StringType,
				},
				"repositories": schema.ListAttribute{
					Description: "A list of repositories to use for the container.",
					Optional:    true,
					ElementType: types.StringType,
				},
				"keyrings": schema.ListAttribute{
					Description: "A list of keyrings to add to the container.",
					Optional:    true,
					ElementType: types.StringType,
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
				"layers": schema.ListNestedAttribute{
					Description: "The list of layers to add to the container.",
					Optional:    true,
					NestedObject: schema.NestedAttributeObject{
						Attributes: map[string]schema.Attribute{
							"source": schema.StringAttribute{
								Description: "The relative or absolute path on the host to the source directory to create a layer from.",
								Required:    true,
							},
							"destination": schema.StringAttribute{
								Description: "The absolute path on the container to root the source directory in.",
								Required:    true,
							},
						},
					},
				},
				"registries": schema.MapNestedAttribute{
					Description: "A map of registries containing configuration for optional auth, tls, and mirror configuration.",
					Optional:    true,
					NestedObject: schema.NestedAttributeObject{
						Attributes: map[string]schema.Attribute{
							"auth": schema.SingleNestedAttribute{
								Optional: true,
								Attributes: map[string]schema.Attribute{
									"username": schema.StringAttribute{
										Optional: true,
									},
									"password": schema.StringAttribute{
										Optional:  true,
										Sensitive: true,
									},
									"auth": schema.StringAttribute{
										Optional: true,
									},
								},
							},
						},
					},
				},
				"resources": schema.SingleNestedAttribute{
					Optional: true,
					Attributes: map[string]schema.Attribute{
						"memory": schema.SingleNestedAttribute{
							Optional: true,
							Attributes: map[string]schema.Attribute{
								"request": schema.StringAttribute{
									Optional:    true,
									Description: "Amount of memory requested for the harness container",
								},
								"limit": schema.StringAttribute{
									Optional:    true,
									Description: "Limit of memory the harness container can consume",
								},
							},
						},
						"cpu": schema.SingleNestedAttribute{
							Optional: true,
							Attributes: map[string]schema.Attribute{
								"request": schema.StringAttribute{
									Optional:    true,
									Description: "Quantity of CPUs requested for the harness container",
								},
								"limit": schema.StringAttribute{
									Optional:    true,
									Description: "Unused.",
								},
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
			},
		),
	}
}
