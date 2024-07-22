package provider

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"os"
	"path/filepath"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/containers/provider"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harnesses/k3s"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/log"
	itypes "github.com/chainguard-dev/terraform-provider-imagetest/internal/types"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/volume"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

var _ resource.ResourceWithModifyPlan = &HarnessK3sResource{}
var _ resource.ResourceWithModifyPlan = &HarnessK3sResource{}

func NewHarnessK3sResource() resource.Resource {
	return &HarnessK3sResource{}
}

// HarnessK3sResource defines the resource implementation.
type HarnessK3sResource struct {
	BaseHarnessResource
	BaseHarnessResource
}

// HarnessK3sResourceModel describes the resource data model.
type HarnessK3sResourceModel struct {
	Id        types.String             `tfsdk:"id"`
	Name      types.String             `tfsdk:"name"`
	Inventory InventoryDataSourceModel `tfsdk:"inventory"`

	Image                types.String                             `tfsdk:"image"`
	DisableCni           types.Bool                               `tfsdk:"disable_cni"`
	DisableNetworkPolicy types.Bool                               `tfsdk:"disable_network_policy"`
	DisableTraefik       types.Bool                               `tfsdk:"disable_traefik"`
	DisableMetricsServer types.Bool                               `tfsdk:"disable_metrics_server"`
	Registries           map[string]RegistryResourceModel         `tfsdk:"registries"`
	Networks             map[string]ContainerResourceModelNetwork `tfsdk:"networks"`
	Sandbox              types.Object                             `tfsdk:"sandbox"`
	Timeouts             timeouts.Value                           `tfsdk:"timeouts"`
	Resources            *ContainerResources                      `tfsdk:"resources"`
	Hooks                *HarnessHooksModel                       `tfsdk:"hooks"`
	KubeletConfig        types.String                             `tfsdk:"kubelet_config"`
}

type RegistryResourceModel struct {
	Auth   *RegistryResourceAuthModel   `tfsdk:"auth"`
	Tls    *RegistryResourceTlsModel    `tfsdk:"tls"`
	Mirror *RegistryResourceMirrorModel `tfsdk:"mirror"`
}

type RegistryResourceMirrorModel struct {
	Endpoints types.List `tfsdk:"endpoints"`
}

type HarnessK3sSandboxResourceModel struct {
	Image      types.String                             `tfsdk:"image"`
	Privileged types.Bool                               `tfsdk:"privileged"`
	Envs       types.Map                                `tfsdk:"envs"`
	Mounts     []ContainerResourceMountModel            `tfsdk:"mounts"`
	Networks   map[string]ContainerResourceModelNetwork `tfsdk:"networks"`
}

func (r *HarnessK3sResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_harness_k3s"
}

func (r *HarnessK3sResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data HarnessK3sResourceModel
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
	if diags.HasError() {
		return
	}
}

func (r *HarnessK3sResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data HarnessK3sResourceModel
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
	if diags.HasError() {
		return
	}
}

func (r *HarnessK3sResource) harness(ctx context.Context, data *HarnessK3sResourceModel) (itypes.Harness, diag.Diagnostics) {
	diags := make(diag.Diagnostics, 0)

	kopts := append([]k3s.Option{
		k3s.WithCniDisabled(data.DisableCni.ValueBool()),
		k3s.WithTraefikDisabled(data.DisableTraefik.ValueBool()),
		k3s.WithMetricsServerDisabled(data.DisableMetricsServer.ValueBool()),
		k3s.WithNetworkPolicyDisabled(data.DisableNetworkPolicy.ValueBool()),
	}, r.workstationOpts()...)

	registries := make(map[string]RegistryResourceModel)
	if data.Registries != nil {
		registries = data.Registries
	}

	networks := make([]string, 0)
	if data.Networks != nil {
		for _, v := range data.Networks {
			networks = append(networks, v.Name.ValueString())
		}
	}

	if r.store.providerResourceData.Harnesses != nil {
		if pc := r.store.providerResourceData.Harnesses.K3s; pc != nil {
			for k, v := range pc.Registries {
				registries[k] = v
			}

			for _, v := range pc.Networks {
				networks = append(networks, v.Name.ValueString())
			}

			if pc.Sandbox != nil {
				if !pc.Sandbox.Image.IsNull() {
					ref, err := name.ParseReference(pc.Sandbox.Image.ValueString())
					if err != nil {
						return nil, []diag.Diagnostic{diag.NewErrorDiagnostic("invalid resource input", fmt.Sprintf("invalid sandbox image reference: %s", err))}
					}
					// will be overridden by resource specific sandbox images if specified
					kopts = append(kopts, k3s.WithSandboxImageRef(ref))
				}
			}
		}
	}

	if !data.Image.IsNull() {
		ref, err := name.ParseReference(data.Image.ValueString())
		if err != nil {
			return nil, []diag.Diagnostic{diag.NewErrorDiagnostic("invalid resource input", fmt.Sprintf("invalid image reference: %s", err))}
		}
		kopts = append(kopts, k3s.WithImageRef(ref))
	}

	if !data.Sandbox.IsNull() {
		sandbox := &HarnessK3sSandboxResourceModel{}
		if diags := data.Sandbox.As(ctx, &sandbox, basetypes.ObjectAsOptions{}); diags.HasError() {
			return nil, diags
		}

		if !sandbox.Image.IsNull() {
			ref, err := name.ParseReference(sandbox.Image.ValueString())
			if err != nil {
				return nil, []diag.Diagnostic{diag.NewErrorDiagnostic("invalid resource input", fmt.Sprintf("invalid sandbox image reference: %s", err))}
			}
			kopts = append(kopts, k3s.WithSandboxImageRef(ref))
		}

		for _, m := range sandbox.Mounts {
			src, err := filepath.Abs(m.Source.ValueString())
			if err != nil {
				return nil, []diag.Diagnostic{diag.NewErrorDiagnostic("invalid resource input", fmt.Sprintf("invalid mount source: %s", err))}
			}

			kopts = append(kopts, k3s.WithSandboxMounts(mount.Mount{
				Type:   mount.TypeBind,
				Source: src,
				Target: m.Destination.ValueString(),
			}))
		}

		for _, n := range sandbox.Networks {
			kopts = append(kopts, k3s.WithSandboxNetworks(n.Name.ValueString()))
		}

		envs := make(map[string]string)
		if diags := sandbox.Envs.ElementsAs(ctx, &envs, false); diags.HasError() {
			return nil, diags
		}
		kopts = append(kopts, k3s.WithSandboxEnv(envs))
	}

	for rname, rdata := range registries {
		if rdata.Auth != nil {
			if rdata.Auth.Auth.IsNull() && rdata.Auth.Password.IsNull() && rdata.Auth.Username.IsNull() {
				kopts = append(kopts, k3s.WithAuthFromKeychain(rname))
			} else {
				kopts = append(kopts, k3s.WithAuthFromStatic(rname, rdata.Auth.Username.ValueString(), rdata.Auth.Password.ValueString(), rdata.Auth.Auth.ValueString()))
			}
		}

		if rdata.Mirror != nil {
			endpoints := make([]string, 0)
			if diags := rdata.Mirror.Endpoints.ElementsAs(ctx, &endpoints, false); diags.HasError() {
				return nil, diags
			}
			kopts = append(kopts, k3s.WithRegistryMirror(rname, endpoints...))
		}
	}

	kopts = append(kopts, k3s.WithNetworks(networks...))

	if res := data.Resources; res != nil {
		rreq, err := ParseResources(res)
		if err != nil {
			return nil, []diag.Diagnostic{diag.NewErrorDiagnostic("failed to parse resources", err.Error())}
		}
		log.Info(ctx, "Setting resources for k3s harness", "cpu_limit", rreq.CpuLimit.String(), "cpu_request", rreq.CpuRequest.String(), "memory_limit", rreq.MemoryLimit.String(), "memory_request", rreq.MemoryRequest.String())
		kopts = append(kopts, k3s.WithResources(rreq))
	}

	if data.Hooks != nil {
		postStarts := []string{}
		if diags := data.Hooks.PostStart.ElementsAs(ctx, &postStarts, false); diags.HasError() {
			return nil, diags
		}

		// Only support PostStarts
		kopts = append(kopts, k3s.WithHooks(k3s.Hooks{
			PostStart: postStarts,
		}))

		if !data.Hooks.PreStart.IsNull() {
			diags = append(diags, diag.NewWarningDiagnostic(
				"PreStart hooks are not supported for k3s harnesses, the configured hooks will not run",
				fmt.Sprintf("PreStart hooks are not supported for k3s harnesses, the configured hooks will not run: %s", data.Hooks.PreStart.String()),
			))
		}
	}

	// if set, configure the harness to expose the k3s api server on some random,
	// unused port, and copy the clusters kubeconfig to the host
	if os.Getenv("IMAGETEST_K3S_KUBECONFIG") != "" {
		kubeconfigPath := os.Getenv("IMAGETEST_K3S_KUBECONFIG")

		// find an unused exposed port
		// NOTE: This isn't concurrency safe, but if we're in this path we're
		// already assumed to not support concurrency
		var port int
		for {
			port = rand.Intn(65535-1024) + 1024
			_, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
			if err != nil {
				break
			}
		}

		diags = append(diags, diag.NewWarningDiagnostic(
			"Using k3s harness dev mode, a single (random) k3s harness is exposed to the host and accessible via the kubeconfig file. This works best if only a single k3s harness is created.",
			fmt.Sprintf(`You have used IMAGETEST_K3S_KUBECONFIG to toggle the k3s harness dev mode.
The k3s harness will expose the apiserver to the host on port "%d", and write the configured kubeconfig to "%s".
You can access the cluster with something like: "KUBECONFIG=%s kubectl get po -A"`, port, kubeconfigPath, kubeconfigPath),
		))

		kopts = append(kopts, k3s.WithHostPort(port), k3s.WithHostKubeconfigPath(kubeconfigPath))
	}

	if !data.KubeletConfig.IsNull() {
		kopts = append(kopts, k3s.WithKubeletConfig(data.KubeletConfig.ValueString()))
	}

	id := data.Id.ValueString()
	configVolumeName := id + "-config"

	if _, err := r.store.cli.VolumeCreate(ctx, volume.CreateOptions{
		Labels: provider.DefaultLabels(),
		Name:   configVolumeName,
	}); err != nil {
		return nil, []diag.Diagnostic{diag.NewErrorDiagnostic("failed to create config volume for k3s harness", err.Error())}
	}

	kopts = append(kopts, k3s.WithContainerVolumeName(configVolumeName))

	harness, err := k3s.New(id, r.store.cli, kopts...)
	if err != nil {
		return nil, []diag.Diagnostic{diag.NewErrorDiagnostic("failed to initialize k3s harness", err.Error())}
	}

	return harness, diags
}

// workstationOpts holds any workstation specific k3s configuration.
func (r *HarnessK3sResource) workstationOpts() []k3s.Option {
	opts := make([]k3s.Option, 0)

	if os.Getenv("WORKSTATION") != "" {
		opts = append(opts, k3s.WithContainerSnapshotter(k3s.K3sContainerSnapshotterNative))
	}

	return opts
}

func (r *HarnessK3sResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	sandboxAttributes := mergeResourceSchemas(
		defaultContainerResourceSchemaAttributes(),
		map[string]schema.Attribute{
			// Override the default image to use one with kubectl instead
			"image": schema.StringAttribute{
				Description: "The full image reference to use for the container.",
				Optional:    true,
			},
		},
	)

	resp.Schema = schema.Schema{
		MarkdownDescription: `A harness that runs steps in a sandbox container networked to a running k3s cluster.`,
		Attributes: mergeResourceSchemas(
			r.schemaAttributes(ctx),
			map[string]schema.Attribute{
				"disable_cni": schema.BoolAttribute{
					Description: "When true, the builtin (flannel) CNI will be disabled.",
					Optional:    true,
					Computed:    true,
					Default:     booldefault.StaticBool(false),
				},
				"disable_traefik": schema.BoolAttribute{
					Description: "When true, the builtin traefik ingress controller will be disabled.",
					Optional:    true,
					Computed:    true,
					Default:     booldefault.StaticBool(true),
				},
				"disable_metrics_server": schema.BoolAttribute{
					Description: "When true, the builtin metrics server will be disabled.",
					Optional:    true,
					Computed:    true,
					Default:     booldefault.StaticBool(true),
				},
				"disable_network_policy": schema.BoolAttribute{
					Description: "When true, the builtin network policy controller will be disabled.",
					Optional:    true,
					Computed:    true,
					Default:     booldefault.StaticBool(false),
				},
				"image": schema.StringAttribute{
					Description: "The full image reference to use for the k3s container.",
					Optional:    true,
				},
				"kubelet_config": schema.StringAttribute{
					Description: "The KubeletConfiguration to be applied to the underlying k3s cluster in YAML format.",
					Optional:    true,
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
							"tls": schema.SingleNestedAttribute{
								Optional: true,
								Attributes: map[string]schema.Attribute{
									"cert_file": schema.StringAttribute{
										Optional: true,
									},
									"key_file": schema.StringAttribute{
										Optional: true,
									},
									"ca_file": schema.StringAttribute{
										Optional: true,
									},
								},
							},
							"mirror": schema.SingleNestedAttribute{
								Optional: true,
								Attributes: map[string]schema.Attribute{
									"endpoints": schema.ListAttribute{
										ElementType: basetypes.StringType{},
										Optional:    true,
									},
								},
							},
						},
					},
				},
				"networks": schema.MapNestedAttribute{
					Description: "A map of existing networks to attach the harness containers to.",
					Optional:    true,
					NestedObject: schema.NestedAttributeObject{
						Attributes: map[string]schema.Attribute{
							"name": schema.StringAttribute{
								Description: "The name of the existing network to attach the harness containers to.",
								Required:    true,
							},
						},
					},
				},
				"sandbox": schema.SingleNestedAttribute{
					Description: "A map of configuration for the sandbox container.",
					Optional:    true,
					Attributes:  sandboxAttributes,
				},
				"resources": schema.SingleNestedAttribute{
					Optional: true,
					Attributes: map[string]schema.Attribute{
						"memory": schema.SingleNestedAttribute{
							Optional: true,
							Attributes: map[string]schema.Attribute{
								"request": schema.StringAttribute{
									Optional:    true,
									Description: "Amount of memory requested for the harness container. The default is the bare minimum required by k3s. Anything lower should be used with caution.",
									Default:     stringdefault.StaticString("2Gi"),
									Computed:    true,
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
									Description: "Amount of memory requested for the harness container",
									Default:     stringdefault.StaticString("1"),
									Computed:    true,
								},
								"limit": schema.StringAttribute{
									Optional:    true,
									Description: "Limit of memory the harness container can consume",
								},
							},
						},
					},
				},
				"hooks": schema.SingleNestedAttribute{
					Optional: true,
					Attributes: map[string]schema.Attribute{
						"pre_start": schema.ListAttribute{
							Description: "Not supported for this harness.",
							Optional:    true,
							ElementType: basetypes.StringType{},
						},
						"post_start": schema.ListAttribute{
							Description: "A list of commands to run after the k3s container successfully starts (the api server is available)",
							Optional:    true,
							ElementType: basetypes.StringType{},
						},
					},
				},
			},
		),
	}
}
