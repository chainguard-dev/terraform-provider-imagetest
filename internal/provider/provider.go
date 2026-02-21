package provider

import (
	"context"
	"maps"
	"os"

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
	Harnesses     *ImageTestProviderHarnessModel `tfsdk:"harnesses"`
	TestExecution *ProviderTestExecutionModel    `tfsdk:"test_execution"`
	Repo          types.String                   `tfsdk:"repo"`
	ExtraRepos    []string                       `tfsdk:"extra_repos"`
	Sandbox       *ProviderSandboxModel          `tfsdk:"sandbox"`
	Logs          *ProviderLogsModel             `tfsdk:"logs"`
}

// ProviderLogsModel describes the logs configuration.
type ProviderLogsModel struct {
	Directory types.String `tfsdk:"directory"`
}

type ImageTestProviderHarnessModel struct {
	K3s     *ProviderHarnessK3sModel     `tfsdk:"k3s"`
	Docker  *ProviderHarnessDockerModel  `tfsdk:"docker"`
	Cluster *ProviderHarnessClusterModel `tfsdk:"cluster"`
}

type ProviderHarnessK3sModel struct {
	Networks   map[string]ContainerNetworkModel `tfsdk:"networks"`
	Registries map[string]RegistryResourceModel `tfsdk:"registries"`
}

type ProviderHarnessClusterModel struct {
	Kubeconfig *string `tfsdk:"kubeconfig"`
}

type ProviderSandboxModel struct {
	ExtraRepos    []string `tfsdk:"extra_repos"`
	ExtraKeyrings []string `tfsdk:"extra_keyrings"`
	ExtraPackages []string `tfsdk:"extra_packages"`
}

type ProviderHarnessDockerModel struct {
	HostSocketPath *string                                `tfsdk:"host_socket_path"`
	Networks       map[string]ContainerNetworkModel       `tfsdk:"networks"`
	Envs           *HarnessContainerEnvs                  `tfsdk:"envs"`
	Mounts         []ContainerMountModel                  `tfsdk:"mounts"`
	Registries     map[string]DockerRegistryResourceModel `tfsdk:"registries"`
}

type ProviderTestExecutionModel struct {
	SkipAll      types.Bool `tfsdk:"skip_all_tests"`
	SkipTeardown types.Bool `tfsdk:"skip_teardown"`
	Include      types.Map  `tfsdk:"include_by_label"`
	Exclude      types.Map  `tfsdk:"exclude_by_label"`
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
						Description:         "Skips all features and harnesses.",
						MarkdownDescription: "Skips all features and harnesses. All tests can also be skipped by setting the environment variable `IMAGETEST_SKIP_ALL` to `true`.",
						Optional:            true,
					},
					"include_by_label": schema.MapAttribute{
						ElementType: types.StringType,
						Description: "Run features with matching label values. Any tests which do not contain all of the provided labels will be skipped.",
						Optional:    true,
					},
					"exclude_by_label": schema.MapAttribute{
						ElementType: types.StringType,
						Description: "Skip features with matching label values. If `include_by_label` is present, the set of included tests are evaluated for skipping.",
						Optional:    true,
					},
					"skip_teardown": schema.BoolAttribute{
						Description:         "Skips the teardown of test harnesses to allow debugging test failures",
						MarkdownDescription: "Skips the teardown of test harnesses to allow debugging test failures. Harness teardown can also be skipped by setting the environment variable `IMAGETEST_SKIP_TEARDOWN` to `true`",
						Optional:            true,
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

	if data.TestExecution == nil {
		data.TestExecution = &ProviderTestExecutionModel{
			Include: types.MapNull(types.StringType),
			Exclude: types.MapNull(types.StringType),
		}
	}

	if v := os.Getenv("IMAGETEST_SKIP_ALL"); v != "" {
		data.TestExecution.SkipAll = basetypes.NewBoolValue(true)
	}

	if v := os.Getenv("IMAGETEST_SKIP_TEARDOWN"); v != "" {
		data.TestExecution.SkipTeardown = basetypes.NewBoolValue(true)
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
	store.skipTeardown = data.TestExecution.SkipTeardown.ValueBool()
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

	// this is a no-op if no otlp endpoint is configured
	if err := o11y.Setup(ctx); err != nil {
		resp.Diagnostics.AddError("failed to setup observability", err.Error())
		return
	}

	// Store any "global" provider configuration in the store
	store.providerResourceData = data

	// Make provider state available to any resources that implement Configure
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
		// Tests
		NewTestDockerRunResource,

		// Tests Resources
		NewTestsResource,

		// Tests for Lambda
		NewTestsLambdaResource,
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
		maps.Copy(result, s)
	}

	return result
}
