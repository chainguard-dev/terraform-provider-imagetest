package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
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
	store   *ProviderStore
}

// ImageTestProviderModel describes the provider data model.
type ImageTestProviderModel struct {
	Harnesses *ImageTestProviderHarnessModel `tfsdk:"harnesses"`
}

type ImageTestProviderHarnessModel struct {
	Container *ProviderHarnessContainerModel `tfsdk:"container"`
	K3s       *ProviderHarnessK3sModel       `tfsdk:"k3s"`
}

type ProviderHarnessContainerModel struct {
	Networks map[string]ContainerResourceModelNetwork `tfsdk:"networks"`
	Envs     types.Map                                `tfsdk:"envs"`
	Mounts   []ContainerResourceMountModel            `tfsdk:"mounts"`
}

type ProviderHarnessK3sModel struct {
	Registries map[string]RegistryResourceModel `tfsdk:"registries"`
}

func (p *ImageTestProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "imagetest"
	resp.Version = p.version
}

func (p *ImageTestProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"harnesses": schema.SingleNestedAttribute{
				Optional: true,
				Attributes: map[string]schema.Attribute{
					"container": schema.SingleNestedAttribute{
						Optional: true,
						Attributes: map[string]schema.Attribute{
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
						},
					},
					"k3s": schema.SingleNestedAttribute{
						Optional: true,
						Attributes: map[string]schema.Attribute{
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

	p.store.providerResourceData = data

	resp.DataSourceData = p.store
	resp.ResourceData = p.store
}

func (p *ImageTestProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewFeatureResource,
		// Harnesses
		NewHarnessTeardownResource,
		NewHarnessK3sResource,
		NewHarnessContainerResource,
	}
}

func (p *ImageTestProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{}
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &ImageTestProvider{
			version: version,
			store:   NewProviderStore(),
		}
	}
}
