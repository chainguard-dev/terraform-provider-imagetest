package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	rschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

var _ provider.Provider = &ImageTestProvider{}

// ImageTestProvider defines the provider implementation.
type ImageTestProvider struct {
	// version is set to the provider version on release, "dev" when the
	// provider is built and ran locally, and "test" when running acceptance
	// testing.
	version string
}

// ImageTestProviderModel describes the provider data model.
type ImageTestProviderModel struct {
	Log       *ProviderLoggerModel           `tfsdk:"log"`
	Harnesses *ImageTestProviderHarnessModel `tfsdk:"harnesses"`
	Labels    types.Map                      `tfsdk:"labels"`
}

type ImageTestProviderHarnessModel struct {
	K3s     *ProviderHarnessK3sModel     `tfsdk:"k3s"`
	Docker  *ProviderHarnessDockerModel  `tfsdk:"docker"`
	Cluster *ProviderHarnessClusterModel `tfsdk:"cluster"`
}

type ProviderHarnessK3sModel struct {
	Networks   map[string]ContainerNetworkModel              `tfsdk:"networks"`
	Registries map[string]RegistryResourceModel              `tfsdk:"registries"`
	Sandbox    *ProviderHarnessContainerSandboxResourceModel `tfsdk:"sandbox"`
}

type ProviderHarnessClusterModel struct {
	Kubeconfig *string `tfsdk:"kubeconfig"`
}

type ProviderHarnessContainerSandboxResourceModel struct {
	Image types.String `tfsdk:"image"`
}

type ProviderHarnessDockerModel struct {
	HostSocketPath *string                                `tfsdk:"host_socket_path"`
	Networks       map[string]ContainerNetworkModel       `tfsdk:"networks"`
	Envs           *HarnessContainerEnvs                  `tfsdk:"envs"`
	Mounts         []ContainerMountModel                  `tfsdk:"mounts"`
	Registries     map[string]DockerRegistryResourceModel `tfsdk:"registries"`
}

type ProviderLoggerModel struct {
	File *ProviderLoggerFileModel `tfsdk:"file"`
}

type ProviderLoggerFileModel struct {
	Directory types.String `tfsdk:"directory"`
	Format    types.String `tfsdk:"format"`
}

func (p *ImageTestProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "imagetest"
	resp.Version = p.version
}

func (p *ImageTestProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"labels": schema.MapAttribute{
				ElementType: types.StringType,
				Optional:    true,
			},
			"log": schema.SingleNestedAttribute{
				Optional: true,
				Attributes: map[string]schema.Attribute{
					"file": schema.SingleNestedAttribute{
						Description: "Output logs to a file.",
						Optional:    true,
						Attributes: map[string]schema.Attribute{
							"format": schema.StringAttribute{
								Description: "The format of the log entries (text|json).",
								Optional:    true,
							},
							"directory": schema.StringAttribute{
								Description: "The directory to write the log file to.",
								Optional:    true,
							},
						},
					},
				},
			},
			"harnesses": schema.SingleNestedAttribute{
				Optional: true,
				Attributes: map[string]schema.Attribute{
					"k3s": schema.SingleNestedAttribute{
						Optional: true,
						Attributes: map[string]schema.Attribute{
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
							"sandbox": schema.SingleNestedAttribute{
								Description: "A map of configuration for the sandbox container.",
								Optional:    true,
								Attributes: map[string]schema.Attribute{
									"image": schema.StringAttribute{
										Description: "The full image reference to use for the container.",
										Optional:    true,
									},
								},
							},
						},
					},
					"docker": schema.SingleNestedAttribute{
						Optional: true,
						Attributes: map[string]schema.Attribute{
							"host_socket_path": schema.StringAttribute{
								Required:    false,
								Optional:    true,
								Description: "The Docker host socket path.",
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
											Description: "The absolute path on the container to mount the source directory to.",
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
						},
					},
					"cluster": schema.SingleNestedAttribute{
						Optional: true,
						Attributes: map[string]schema.Attribute{
							"kubeconfig": schema.StringAttribute{
								Description: "The relative or absolute path on the host to the source directory to mount.",
								Required:    true,
							},
						},
					},
				},
			},
		},
	}
}

func (p *ImageTestProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var data ImageTestProviderModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	store := NewProviderStore()

	labels := make(map[string]string)
	if diag := data.Labels.ElementsAs(ctx, &labels, false); diag.HasError() {
		return
	}
	store.labels = labels

	// Store any "global" provider configuration in the store
	store.providerResourceData = data

	resp.DataSourceData = store
	resp.ResourceData = store
}

func (p *ImageTestProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewFeatureResource,
		NewContainerVolumeResource,
		// Harnesses
		NewHarnessK3sResource,
		NewHarnessDockerResource,
		NewHarnessPterraformResource,
	}
}

func (p *ImageTestProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewInventoryDataSource,
	}
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &ImageTestProvider{
			version: version,
		}
	}
}

// mergeResourceSchemas merges the given schemas into a single map. priority is last to
// first.
func mergeResourceSchemas(schemas ...map[string]rschema.Attribute) map[string]rschema.Attribute {
	result := make(map[string]rschema.Attribute)

	for _, s := range schemas {
		for k, v := range s {
			result[k] = v
		}
	}

	return result
}
