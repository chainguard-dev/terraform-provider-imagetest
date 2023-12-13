package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
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
type ImageTestProviderModel struct{}

func (p *ImageTestProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "imagetest"
	resp.Version = p.version
}

func (p *ImageTestProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{},
	}
}

func (p *ImageTestProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var data ImageTestProviderModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	resp.DataSourceData = p.store
	resp.ResourceData = p.store
}

func (p *ImageTestProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewFeatureResource,
		// Harnesses
		NewHarnessNullResource,
		NewHarnessK3sResource,
		NewHarnessTeardownResource,
		// Environments
		NewEnvironmentResource,
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
