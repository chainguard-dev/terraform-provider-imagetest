package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/bundler"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/entrypoint"
	internallog "github.com/chainguard-dev/terraform-provider-imagetest/internal/log"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/provider/framework"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/skip"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type contextKey string

const (
	contextKeyResourceTestID    contextKey = "resource_test_id"
	TestsResourceDefaultTimeout string     = "30m"
	TestResourceDefaultTimeout  string     = "15m"
)

var _ resource.ResourceWithConfigure = &TestsResource{}

func NewTestsResource() resource.Resource {
	return &TestsResource{WithTypeName: "tests"}
}

type TestsResource struct {
	framework.WithTypeName
	framework.WithNoOpDelete
	framework.WithNoOpRead

	repo             name.Repository   // The primary target_repository used for publishing test sandboxes
	extraRepos       []name.Repository // Extra repositories to wire auth creds into drivers
	ropts            []remote.Option
	entrypointLayers map[string][]v1.Layer
	includeTests     map[string]string
	excludeTests     map[string]string
	logsDirectory    string
}

type TestsResourceModel struct {
	Id           types.String               `tfsdk:"id"`
	Name         types.String               `tfsdk:"name"`
	Driver       DriverResourceModel        `tfsdk:"driver"`
	Drivers      *TestsDriversResourceModel `tfsdk:"drivers"`
	Images       TestsImageResource         `tfsdk:"images"`
	Tests        []*TestResourceModel       `tfsdk:"tests"`
	Timeout      types.String               `tfsdk:"timeout"`
	Labels       map[string]string          `tfsdk:"labels"`
	Skipped      types.Bool                 `tfsdk:"skipped"`
	RepoOverride types.String               `tfsdk:"repo"`
}

type TestsImageResource map[string]string

func (t TestsImageResource) Resolve() (map[string]TestsImagesParsed, error) {
	pimgs := make(map[string]TestsImagesParsed)
	for k, v := range t {
		ref, err := name.ParseReference(v)
		if err != nil {
			return nil, fmt.Errorf("failed to parse reference: %w", err)
		}

		if _, ok := ref.(name.Tag); ok {
			return nil, fmt.Errorf("tag references are not supported")
		}

		pimgs[k] = TestsImagesParsed{
			Registry:     ref.Context().RegistryStr(),
			Repo:         ref.Context().RepositoryStr(),
			RegistryRepo: ref.Context().RegistryStr() + "/" + ref.Context().RepositoryStr(),
			Digest:       ref.Identifier(),
			PseudoTag:    fmt.Sprintf("unused@%s", ref.Identifier()),
			Ref:          ref.String(),
		}
	}
	return pimgs, nil
}

type TestResourceModel struct {
	Name     types.String               `tfsdk:"name"`
	Image    types.String               `tfsdk:"image"`
	Content  []TestContentResourceModel `tfsdk:"content"`
	Envs     map[string]string          `tfsdk:"envs"`
	Cmd      types.String               `tfsdk:"cmd"`
	Timeout  types.String               `tfsdk:"timeout"`
	Artifact types.Object               `tfsdk:"artifact"`
}

type TestContentResourceModel struct {
	Source types.String `tfsdk:"source"`
	Target types.String `tfsdk:"target"`
}

type TestArtifactResourceModel struct {
	URI      types.String `tfsdk:"uri"`
	Checksum types.String `tfsdk:"checksum"`
}

var testArtifactAttTypes = map[string]attr.Type{
	"uri":      types.StringType,
	"checksum": types.StringType,
}

func (t *TestsResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: ``,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The unique identifier for the test. If a name is provided, this will be the name appended with a random suffix.",
				Computed:    true,
			},
			"name": schema.StringAttribute{
				Description: "The name of the test. If one is not provided, a random name will be generated.",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("test"),
			},
			"driver": schema.StringAttribute{
				Description: "The driver to use for the test suite. Only one driver can be used at a time.",
				Required:    true,
			},
			"repo": schema.StringAttribute{
				Optional:    true,
				Description: "The target repository the provider will use for pushing/pulling dynamically built images, overriding provider config.",
			},
			"drivers": DriverResourceSchema(ctx),
			"images": schema.MapAttribute{
				ElementType: types.StringType,
				Required:    true,
				Description: "Images to use for the test suite.",
			},
			"tests": schema.ListNestedAttribute{
				Description: "An ordered list of test suites to run",
				Optional:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Description: "The name of the test",
							Required:    true,
						},
						"image": schema.StringAttribute{
							Description: "The image reference to use as the base image for the test.",
							Required:    true,
						},
						"content": schema.ListNestedAttribute{
							Description: "The content to use for the test",
							Optional:    true,
							NestedObject: schema.NestedAttributeObject{
								Attributes: map[string]schema.Attribute{
									"source": schema.StringAttribute{
										Description: "The source path to use for the test",
										Required:    true,
									},
									"target": schema.StringAttribute{
										Description: "The target path to use for the test",
										Optional:    true,
									},
								},
							},
						},
						"cmd": schema.StringAttribute{
							Description: "When specified, will override the sandbox image's CMD (oci config).",
							Optional:    true,
						},
						"envs": schema.MapAttribute{
							Description: "Environment variables to set on the test container. These will overwrite the environment variables set in the image's config on conflicts.",
							Optional:    true,
							ElementType: types.StringType,
						},
						"timeout": schema.StringAttribute{
							Description: "The maximum amount of time to wait for the individual test to complete. This is encompassed by the overall timeout of the parent tests resource.",
							Optional:    true,
						},
						"artifact": schema.SingleNestedAttribute{
							Description: "The bundled artifact generated by the test.",
							Optional:    true,
							Computed:    true,
							Attributes: map[string]schema.Attribute{
								"uri": schema.StringAttribute{
									Description: "The URI of the artifact. The artifact is in targz format.",
									Computed:    true,
								},
								"checksum": schema.StringAttribute{
									Description: "The checksum of the artifact.",
									Computed:    true,
								},
							},
						},
					},
				},
			},
			"timeout": schema.StringAttribute{
				Description: "The maximum amount of time to wait for all tests to complete. This includes the time it takes to start and destroy the driver.",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString(TestsResourceDefaultTimeout),
			},
			"labels": schema.MapAttribute{
				Description: "Metadata to attach to the tests resource. Used for filtering and grouping.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"skipped": schema.BoolAttribute{
				Description: "Whether or not the tests were skipped. This is set to true if the tests were skipped, and false otherwise.",
				Optional:    true,
				Computed:    true,
			},
		},
	}
}

func (t *TestsResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	store, ok := req.ProviderData.(*ProviderStore)
	if !ok {
		resp.Diagnostics.AddError("invalid provider data", "...")
		return
	}

	t.repo = store.repo
	t.extraRepos = store.extraRepos
	t.ropts = store.ropts
	t.entrypointLayers = store.entrypointLayers
	t.includeTests = store.includeTests
	t.excludeTests = store.excludeTests
	t.logsDirectory = store.logsDirectory
}

func (t *TestsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data TestsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(t.do(ctx, &data)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (t *TestsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data TestsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(t.do(ctx, &data)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (t *TestsResource) do(ctx context.Context, data *TestsResourceModel) (ds diag.Diagnostics) {
	// lightly sanitize the name, this likely needs some revision
	id := strings.ReplaceAll(fmt.Sprintf("%s-%s-%s", data.Name.ValueString(), data.Driver, uuid.New().String()[:4]), " ", "_")
	data.Id = types.StringValue(id)

	// Set up basic logging without file teeing (that happens per test)
	ctx = clog.WithLogger(ctx, clog.New(slog.Default().Handler()))
	ctx = clog.WithValues(ctx,
		"test_id", id,
		"test_name", data.Name.ValueString(),
		"driver", data.Driver,
	)

	// Store test_id in context to deconflict with other tests
	ctx = context.WithValue(ctx, contextKeyResourceTestID, id)

	for _, test := range data.Tests {
		if test.Artifact.IsNull() || test.Artifact.IsUnknown() {
			emptyArtifact := map[string]attr.Value{
				"uri":      types.StringNull(),
				"checksum": types.StringNull(),
			}
			artifactObj, objDiags := types.ObjectValue(testArtifactAttTypes, emptyArtifact)
			ds.Append(objDiags...)
			test.Artifact = artifactObj
		}
	}

	_skip, reason := skip.Skip(data.Labels, t.includeTests, t.excludeTests)
	if v := os.Getenv("IMAGETEST_SKIP_ALL"); v != "" {
		_skip = true
		reason = "IMAGETEST_SKIP_ALL is set"
	}
	data.Skipped = types.BoolValue(_skip)

	if data.Skipped.ValueBool() {
		return []diag.Diagnostic{
			diag.NewWarningDiagnostic(
				fmt.Sprintf("skipping tests [%s]", id),
				fmt.Sprintf("test is skipped: %s", reason)),
		}
	}

	timeout, err := time.ParseDuration(data.Timeout.ValueString())
	if err != nil {
		return []diag.Diagnostic{diag.NewErrorDiagnostic("failed to parse timeout", err.Error())}
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	t.ropts = append(t.ropts, remote.WithContext(ctx))

	imgsResolved, err := data.Images.Resolve()
	if err != nil {
		return []diag.Diagnostic{diag.NewErrorDiagnostic("failed to resolve images", err.Error())}
	}

	imgsResolvedData, err := json.Marshal(imgsResolved)
	if err != nil {
		return []diag.Diagnostic{diag.NewErrorDiagnostic("failed to resolve images", err.Error())}
	}
	clog.InfoContext(ctx, "resolved images", "images", string(imgsResolvedData))

	// we should never get here, but just in case
	if t.entrypointLayers == nil {
		return []diag.Diagnostic{diag.NewErrorDiagnostic("invalid entrypoint image provided", "")}
	}

	repo := t.repo
	if data.RepoOverride.ValueString() != "" {
		clog.InfoContextf(ctx, "using repository override %q", data.RepoOverride.String())
		var err error
		repo, err = name.NewRepository(data.RepoOverride.ValueString())
		if err != nil {
			return []diag.Diagnostic{diag.NewErrorDiagnostic("failed to parse repo override", err.Error())}
		}
	}

	trepo, err := name.NewRepository(fmt.Sprintf("%s/%s", repo.String(), "imagetest"))
	if err != nil {
		return []diag.Diagnostic{diag.NewErrorDiagnostic("failed to create target repository", err.Error())}
	}

	trefs := make([]name.Reference, 0, len(data.Tests))
	for _, test := range data.Tests {
		l := clog.FromContext(ctx).With("test_name", test.Name.ValueString(), "test_id", id)
		l.InfoContext(ctx, "starting test")

		// for each test, we build the test image. The test image is assembled
		// using a combination of the user provided "base" image, the entrypoint
		// image, and the user provided test contents. Fully assembled, the layers
		// looks something like:
		//
		// 0: The test image
		// 1: The entrypoint image
		// 2: The test content
		//
		// The entrypoint image supports linux/arm64 and linux/amd64 architectures.
		// This accommodates for either single or multiarch test images,
		// but there must be at _least_ a linux/arm64 or linux/amd64 variant. The
		// test content is assumed to be architecture independent (source files),
		// but we do not check. This may lead to runtime errors if a user is
		// attempting to assemble runtime tools, but for now we'll combat that with
		// documentation.
		//
		// The resulting name.Reference will depend on whether the base image is an
		// index or an image.

		baseref, err := name.ParseReference(test.Image.ValueString())
		if err != nil {
			return []diag.Diagnostic{diag.NewErrorDiagnostic("failed to parse base image reference", err.Error())}
		}

		// We assume, but do not check, that the test contents are architecture independent
		sls := make([]v1.Layer, 0, len(test.Content))
		for _, c := range test.Content {
			target := c.Target.ValueString()
			if target == "" {
				target = entrypoint.DefaultWorkDir
			}

			layer, err := bundler.NewLayerFromPath(c.Source.ValueString(), target)
			if err != nil {
				return []diag.Diagnostic{diag.NewErrorDiagnostic("failed to create layer", err.Error())}
			}
			sls = append(sls, layer)
		}

		tref, err := bundler.Mutate(ctx, baseref, trepo, bundler.MutateOpts{
			RemoteOptions: t.ropts,
			ImageMutators: []func(v1.Image) (v1.Image, error){
				// Mutator to append the arch specific entrypoint layers
				func(base v1.Image) (v1.Image, error) {
					cfg, err := base.ConfigFile()
					if err != nil {
						return nil, fmt.Errorf("failed to get config file: %w", err)
					}

					clog.InfoContext(ctx, "using entrypoint layers", "platform", cfg.Platform())
					el, ok := t.entrypointLayers[cfg.Platform().Architecture]
					if !ok {
						return base, nil
					}

					return mutate.AppendLayers(base, el...)
				},
				// Mutator to append the test source layers
				func(base v1.Image) (v1.Image, error) {
					return mutate.AppendLayers(base, sls...)
				},
				// Mutator to rejigger the final image config
				func(img v1.Image) (v1.Image, error) {
					cfgf, err := img.ConfigFile()
					if err != nil {
						return nil, fmt.Errorf("failed to get config file: %w", err)
					}

					envs := make(map[string]string)
					maps.Copy(envs, test.Envs)
					envs["IMAGES"] = string(imgsResolvedData)
					envs["IMAGETEST_DRIVER"] = string(data.Driver)
					envs["IMAGETEST_REGISTRY"] = trepo.RegistryStr()
					envs["IMAGETEST_REPO"] = trepo.String()
					envs[entrypoint.AritfactsDirEnvVar] = entrypoint.ArtifactsDir

					if os.Getenv("IMAGETEST_SKIP_TEARDOWN") != "" {
						envs[entrypoint.PauseModeEnvVar] = string(entrypoint.PauseAlways)
					}

					if os.Getenv("IMAGETEST_SKIP_TEARDOWN_ON_FAILURE") != "" {
						envs[entrypoint.PauseModeEnvVar] = string(entrypoint.PauseOnError)
					}

					// Add some extra env vars for the entrypoint to potentially key off of
					if isLocalRegistry(trepo.Registry) {
						clog.InfoContext(ctx, "using local registry", "registry", trepo.RegistryStr())

						u, err := url.Parse("http://" + trepo.RegistryStr())
						if err != nil {
							return nil, fmt.Errorf("failed to parse registry url: %w", err)
						}

						envs[entrypoint.DriverLocalRegistryEnvVar] = "1"
						envs[entrypoint.DriverLocalRegistryHostnameEnvVar] = u.Hostname()
						envs[entrypoint.DriverLocalRegistryPortEnvVar] = u.Port()
					}

					if cfgf.Config.Env == nil {
						cfgf.Config.Env = make([]string, 0)
					}

					for k, v := range envs {
						cfgf.Config.Env = append(cfgf.Config.Env, fmt.Sprintf("%s=%s", k, v))
					}

					// Use a standard entrypoint
					cfgf.Config.Entrypoint = entrypoint.DefaultEntrypoint

					cfgf.Config.Cmd = []string{test.Cmd.ValueString()}

					if cfgf.Config.WorkingDir == "" {
						cfgf.Config.WorkingDir = entrypoint.DefaultWorkDir
					}

					cfgf.Config.User = "0:0"

					return mutate.ConfigFile(img, cfgf)
				},
				// Mutator to annotate the image with the test name
				func(i v1.Image) (v1.Image, error) {
					img, ok := mutate.Annotations(i, map[string]string{
						"imagetest.test_name": test.Name.ValueString(),
					}).(v1.Image)
					if !ok {
						return nil, fmt.Errorf("failed to assert mutate.Annotations result as v1.Image")
					}
					return img, nil
				},
			},
		})
		if err != nil {
			return []diag.Diagnostic{diag.NewErrorDiagnostic("failed to mutate test image", err.Error())}
		}

		clog.InfoContext(ctx, fmt.Sprintf("build test image [%s]", tref.String()), "test_name", test.Name.ValueString(), "test_id", id)
		trefs = append(trefs, tref)
	}

	dr, err := t.LoadDriver(ctx, data.Drivers, data.Driver, data.Id.ValueString(), data.Timeout.ValueString())
	if err != nil {
		return []diag.Diagnostic{diag.NewErrorDiagnostic("failed to load driver", err.Error())}
	}

	defer func() {
		if teardownErr := t.maybeTeardown(ctx, dr, ds.HasError()); teardownErr != nil {
			ds = append(ds, teardownErr)
		}
	}()

	clog.InfoContext(ctx, "setting up driver")
	if err := dr.Setup(ctx); err != nil {
		return []diag.Diagnostic{diag.NewErrorDiagnostic("failed to setup driver", err.Error())}
	}

	for i, tref := range trefs {
		ds.Append(t.doTest(ctx, dr, data.Tests[i], tref)...)
		if ds.HasError() {
			return ds
		}
	}

	return ds
}

func (t *TestsResource) doTest(ctx context.Context, d drivers.Tester, test *TestResourceModel, ref name.Reference) diag.Diagnostics {
	// Get the test_id from context
	testID, ok := ctx.Value(contextKeyResourceTestID).(string)
	if !ok {
		return []diag.Diagnostic{diag.NewErrorDiagnostic("internal error", "test_id not found in context")}
	}
	testName := test.Name.ValueString()

	// Set up logging with file teeing if configured
	ctx, cleanup := internallog.SetupTestsLogging(ctx, t.logsDirectory, testID, testName)
	defer cleanup()

	ctx = clog.WithValues(ctx,
		"test_name", test.Name.ValueString(),
		"test_ref", ref.String(),
	)

	diags := diag.Diagnostics{}

	timeout := TestResourceDefaultTimeout
	if test.Timeout.ValueString() != "" {
		timeout = test.Timeout.ValueString()
	}

	tduration, err := time.ParseDuration(timeout)
	if err != nil {
		diags.Append(diag.NewWarningDiagnostic("failed to parse timeout, using the default", err.Error()))
		td, err := time.ParseDuration(TestResourceDefaultTimeout)
		if err != nil {
			return diags
		}
		tduration = td
	}

	ctx, cancel := context.WithTimeout(ctx, tduration)
	defer cancel()

	artifact := map[string]attr.Value{
		"uri":      types.StringNull(),
		"checksum": types.StringNull(),
	}

	result, err := d.Run(ctx, ref)
	if result != nil && result.Artifact != nil {
		artifact["uri"] = types.StringValue(result.Artifact.URI)
		artifact["checksum"] = types.StringValue(result.Artifact.Checksum)

		artifactObj, objDiags := types.ObjectValue(testArtifactAttTypes, artifact)
		diags.Append(objDiags...)
		test.Artifact = artifactObj
	}

	if err != nil {
		diags.Append(diag.NewErrorDiagnostic("failed to run test", err.Error()))
		return diags
	}

	// Return the diags we've been filling with warnings
	return diags
}

func (t *TestsResource) maybeTeardown(ctx context.Context, d drivers.Tester, failed bool) diag.Diagnostic {
	if v := os.Getenv("IMAGETEST_SKIP_TEARDOWN"); v != "" {
		return diag.NewWarningDiagnostic("skipping teardown", "IMAGETEST_SKIP_TEARDOWN is set, skipping teardown")
	}

	if v := os.Getenv("IMAGETEST_SKIP_TEARDOWN_ON_FAILURE"); v != "" && failed {
		return diag.NewWarningDiagnostic("skipping teardown", "IMAGETEST_SKIP_TEARDOWN_ON_FAILURE is set and test failed, skipping teardown")
	}

	if err := d.Teardown(ctx); err != nil {
		return diag.NewWarningDiagnostic("failed to teardown test driver", err.Error())
	}

	return nil
}

type TestsImagesParsed struct {
	Registry     string `json:"registry"`
	Repo         string `json:"repo"`
	RegistryRepo string `json:"registry_repo"`
	Digest       string `json:"digest"`
	PseudoTag    string `json:"pseudo_tag"`
	Ref          string `json:"ref"`
}
