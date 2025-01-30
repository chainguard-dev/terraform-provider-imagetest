package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/bundler"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/entrypoint"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/provider/framework"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const (
	TestsResourceDefaultTimeout = "30m"
	TestResourceDefaultTimeout  = "15m"
)

var _ resource.ResourceWithConfigure = &TestsResource{}

func NewTestsResource() resource.Resource {
	return &TestsResource{WithTypeName: "tests"}
}

type TestsResource struct {
	framework.WithTypeName
	framework.WithNoOpDelete
	framework.WithNoOpRead

	repo             name.Repository
	ropts            []remote.Option
	entrypointLayers map[string][]v1.Layer
}

type TestsResourceModel struct {
	Id      types.String               `tfsdk:"id"`
	Name    types.String               `tfsdk:"name"`
	Driver  DriverResourceModel        `tfsdk:"driver"`
	Drivers *TestsDriversResourceModel `tfsdk:"drivers"`
	Images  TestsImageResource         `tfsdk:"images"`
	Tests   []TestResourceModel        `tfsdk:"tests"`
	Timeout types.String               `tfsdk:"timeout"`
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
	Name    types.String               `tfsdk:"name"`
	Image   types.String               `tfsdk:"image"`
	Content []TestContentResourceModel `tfsdk:"content"`
	Envs    map[string]string          `tfsdk:"envs"`
	Cmd     types.String               `tfsdk:"cmd"`
	Timeout types.String               `tfsdk:"timeout"`
}

type TestContentResourceModel struct {
	Source types.String `tfsdk:"source"`
	Target types.String `tfsdk:"target"`
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
					},
				},
			},
			"timeout": schema.StringAttribute{
				Description: "The maximum amount of time to wait for all tests to complete. This includes the time it takes to start and destroy the driver.",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString(TestsResourceDefaultTimeout),
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
	t.ropts = store.ropts
	t.entrypointLayers = store.entrypointLayers
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
	ctx = clog.WithLogger(ctx, clog.New(slog.Default().Handler()))

	timeout, err := time.ParseDuration(data.Timeout.ValueString())
	if err != nil {
		return []diag.Diagnostic{diag.NewErrorDiagnostic("failed to parse timeout", err.Error())}
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	t.ropts = append(t.ropts, remote.WithContext(ctx))

	// lightly sanitize the name, this likely needs some revision
	id := strings.ReplaceAll(fmt.Sprintf("%s-%s-%s", data.Name.ValueString(), data.Driver, uuid.New().String()[:4]), " ", "_")
	data.Id = types.StringValue(id)

	l := clog.FromContext(ctx).With(
		"test_id", id,
		"driver_name", data.Driver,
	)

	imgsResolved, err := data.Images.Resolve()
	if err != nil {
		return []diag.Diagnostic{diag.NewErrorDiagnostic("failed to resolve images", err.Error())}
	}

	imgsResolvedData, err := json.Marshal(imgsResolved)
	if err != nil {
		return []diag.Diagnostic{diag.NewErrorDiagnostic("failed to resolve images", err.Error())}
	}
	l.InfoContext(ctx, "resolved images", "images", string(imgsResolvedData))

	// we should never get here, but just in case
	if t.entrypointLayers == nil {
		return []diag.Diagnostic{diag.NewErrorDiagnostic("invalid entrypoint image provided", "")}
	}

	trepo, err := name.NewRepository(fmt.Sprintf("%s/%s", t.repo.String(), "imagetest"))
	if err != nil {
		return []diag.Diagnostic{diag.NewErrorDiagnostic("failed to create target repository", err.Error())}
	}

	trefs := make([]name.Reference, 0, len(data.Tests))
	for _, test := range data.Tests {
		l := l.With("test_name", test.Name.ValueString(), "test_id", id)
		l.InfoContext(ctx, "starting test", "driver", data.Driver)

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
				target = "/imagetest"
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
					for k, v := range test.Envs {
						envs[k] = v
					}
					envs["IMAGES"] = string(imgsResolvedData)
					envs["IMAGETEST_DRIVER"] = string(data.Driver)

					if os.Getenv("IMAGETEST_SKIP_TEARDOWN_ON_FAILURE") != "" || os.Getenv("IMAGETEST_SKIP_TEARDOWN") != "" {
						envs["IMAGETEST_PAUSE_ON_ERROR"] = "true"
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
						cfgf.Config.WorkingDir = "/imagetest"
					}

					cfgf.Config.User = "0:0"

					return mutate.ConfigFile(img, cfgf)
				},
			},
		})
		if err != nil {
			return []diag.Diagnostic{diag.NewErrorDiagnostic("failed to mutate test image", err.Error())}
		}

		clog.InfoContext(ctx, fmt.Sprintf("build test image [%s]", tref.String()), "test_name", test.Name.ValueString(), "test_id", id)
		trefs = append(trefs, tref)
	}

	dr, err := t.LoadDriver(ctx, data.Drivers, data.Driver, data.Id.ValueString())
	if err != nil {
		return []diag.Diagnostic{diag.NewErrorDiagnostic("failed to load driver", err.Error())}
	}

	defer func() {
		if teardownErr := t.maybeTeardown(ctx, dr, ds.HasError()); teardownErr != nil {
			ds = append(ds, teardownErr)
		}
	}()

	l.InfoContext(ctx, "setting up driver")
	if err := dr.Setup(ctx); err != nil {
		return []diag.Diagnostic{diag.NewErrorDiagnostic("failed to setup driver", err.Error())}
	}

	for i, tref := range trefs {
		tlog := clog.FromContext(ctx).With("test_name", data.Tests[i].Name.ValueString(), "test_ref", tref.String())

		t := data.Tests[i].Timeout.ValueString()
		if t == "" {
			t = TestResourceDefaultTimeout
		}

		ttimeout, err := time.ParseDuration(t)
		if err != nil {
			return []diag.Diagnostic{diag.NewErrorDiagnostic("failed to parse timeout", err.Error())}
		}

		func() {
			tctx, tcancel := context.WithTimeout(ctx, ttimeout)
			defer tcancel()

			tctx = clog.WithLogger(tctx, tlog)
			if err := dr.Run(tctx, tref); err != nil {
				ds.AddError(
					fmt.Sprintf("test [%s/%s] (%s) failed", data.Id.ValueString(), data.Tests[i].Name.ValueString(), tref.String()),
					err.Error(),
				)
				return
			}
		}()

		if ds.HasError() {
			return ds
		}
	}

	return ds
}

func (t *TestsResource) maybeTeardown(ctx context.Context, d drivers.Tester, failed bool) diag.Diagnostic {
	if v := os.Getenv("IMAGETEST_SKIP_TEARDOWN"); v != "" {
		return diag.NewWarningDiagnostic("skipping teardown", "IMAGETEST_SKIP_TEARDOWN is set, skipping teardown")
	}

	if v := os.Getenv("IMAGETEST_SKIP_TEARDOWN_ON_FAILURE"); v != "" && failed {
		return diag.NewWarningDiagnostic("skipping teardown", "IMAGETEST_SKIP_TEARDOWN_ON_FAILURE is set and test failed, skipping teardown")
	}

	if err := d.Teardown(ctx); err != nil {
		return diag.NewErrorDiagnostic("failed to teardown test driver", err.Error())
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
