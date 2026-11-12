package provider

import (
	"context"
	"maps"
	"os"
	"time"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/o11y"
	"github.com/google/go-containerregistry/pkg/name"
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

	// repo exists strictly for testing
	repo string
}

// ImageTestProviderModel describes the provider data model.
type ImageTestProviderModel struct {
	TestExecution *ProviderTestExecutionModel `tfsdk:"test_execution"`
	Repo          types.String                `tfsdk:"repo"`
	ExtraRepos    []string                    `tfsdk:"extra_repos"`
	Sandbox       *ProviderSandboxModel       `tfsdk:"sandbox"`
	Logs          *ProviderLogsModel          `tfsdk:"logs"`
}

// ProviderLogsModel describes the logs configuration.
type ProviderLogsModel struct {
	Directory types.String `tfsdk:"directory"`
}

type ProviderSandboxModel struct {
	ExtraRepos    []string `tfsdk:"extra_repos"`
	ExtraKeyrings []string `tfsdk:"extra_keyrings"`
	ExtraPackages []string `tfsdk:"extra_packages"`
}

type ProviderTestExecutionModel struct {
	SkipAll types.Bool `tfsdk:"skip_all_tests"`
	Include types.Map  `tfsdk:"include_by_label"`
	Exclude types.Map  `tfsdk:"exclude_by_label"`
	// TODO: Global timeout, retry, etc
}

func (p *ImageTestProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "imagetest"
	resp.Version = p.version
}

func (p *ImageTestProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"repo": schema.StringAttribute{
				Optional:    true,
				Description: "The target repository the provider will use for pushing/pulling dynamically built images.",
			},
			"extra_repos": schema.ListAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "An optional list of extra oci registries to wire in auth credentials for.",
			},
			"test_execution": schema.SingleNestedAttribute{
				Optional: true,
				Attributes: map[string]schema.Attribute{
					"skip_all_tests": schema.BoolAttribute{
						Description:         "Skips all `imagetest_test_docker_run` tests.",
						MarkdownDescription: "Skips all `imagetest_test_docker_run` tests. Setting the environment variable `IMAGETEST_SKIP_ALL` to `true` skips these and all `imagetest_tests` tests.",
						Optional:            true,
					},
					"include_by_label": schema.MapAttribute{
						ElementType: types.StringType,
						Description: "Run tests with matching label values. Any tests which do not contain all of the provided labels will be skipped.",
						Optional:    true,
					},
					"exclude_by_label": schema.MapAttribute{
						ElementType: types.StringType,
						Description: "Skip tests with matching label values. If `include_by_label` is present, the set of included tests are evaluated for skipping.",
						Optional:    true,
					},
				},
			},
			"sandbox": schema.SingleNestedAttribute{
				Description: "The optional configuration for all test sandboxes.",
				Optional:    true,
				Attributes: map[string]schema.Attribute{
					"extra_repos": schema.ListAttribute{
						Description: "A list of additional repositories to use for the sandbox.",
						Optional:    true,
						ElementType: types.StringType,
					},
					"extra_keyrings": schema.ListAttribute{
						Description: "A list of additional keyrings to use for the sandbox.",
						Optional:    true,
						ElementType: types.StringType,
					},
					"extra_packages": schema.ListAttribute{
						Description: "A list of additional packages to use for the sandbox.",
						Optional:    true,
						ElementType: types.StringType,
					},
				},
			},
			"logs": schema.SingleNestedAttribute{
				Description: "Configuration for test log output to files.",
				Optional:    true,
				Attributes: map[string]schema.Attribute{
					"directory": schema.StringAttribute{
						Description: "Base directory where test logs will be written. Each test resource creates its own subdirectory. Can be overridden by IMAGETEST_LOGS environment variable.",
						Optional:    true,
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

	if data.TestExecution == nil {
		data.TestExecution = &ProviderTestExecutionModel{
			Include: types.MapNull(types.StringType),
			Exclude: types.MapNull(types.StringType),
		}
	}

	if v := os.Getenv("IMAGETEST_SKIP_ALL"); v != "" {
		data.TestExecution.SkipAll = basetypes.NewBoolValue(true)
	}

	var repo name.Repository
	if p.repo != "" {
		r, err := name.NewRepository(p.repo)
		if err != nil {
			resp.Diagnostics.AddError("invalid repository", err.Error())
			return
		}
		repo = r
	}

	if data.Repo.ValueString() != "" {
		r, err := name.NewRepository(data.Repo.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("invalid repository", err.Error())
			return
		}
		repo = r
	}

	if data.Sandbox == nil {
		data.Sandbox = &ProviderSandboxModel{
			ExtraRepos:    []string{},
			ExtraKeyrings: []string{},
			ExtraPackages: []string{},
		}
	}

	store, err := NewProviderStore(repo)
	if err != nil {
		resp.Diagnostics.AddError("failed to create provider store", err.Error())
		return
	}

	for _, repo := range data.ExtraRepos {
		r, err := name.NewRepository(repo)
		if err != nil {
			resp.Diagnostics.AddError("invalid extra repository", err.Error())
			return
		}
		store.extraRepos = append(store.extraRepos, r)
	}

	store.skipAll = data.TestExecution.SkipAll.ValueBool()
	if diag := data.TestExecution.Include.ElementsAs(ctx, &store.includeTests, true); diag.HasError() {
		resp.Diagnostics.Append(diag...)
		return
	}
	if diag := data.TestExecution.Exclude.ElementsAs(ctx, &store.excludeTests, true); diag.HasError() {
		resp.Diagnostics.Append(diag...)
		return
	}

	// Store logs configuration if provided
	if data.Logs != nil && !data.Logs.Directory.IsNull() {
		store.logsDirectory = data.Logs.Directory.ValueString()
	}

	// Check for environment variable override
	if v := os.Getenv("IMAGETEST_LOGS"); v != "" {
		store.logsDirectory = v
	}

	// Store any "global" provider configuration in the store
	store.providerResourceData = data

	// Make provider state available to any resources that implement Configure
	resp.DataSourceData = store
	resp.ResourceData = store
}

func (p *ImageTestProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		// Tests
		NewTestDockerRunResource,

		// Tests Resources
		NewTestsResource,

		// Tests for Lambda
		NewTestsLambdaResource,
	}
}

func (p *ImageTestProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{}
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
		maps.Copy(result, s)
	}

	return result
}

// Terraform may stop the provider immediately after the apply response.
func flushTelemetry(ctx context.Context) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := o11y.Flush(ctx); err != nil {
		clog.WarnContext(ctx, "otel flush", "error", err)
	}
}
