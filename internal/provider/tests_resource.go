package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/bundler"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/provider/framework"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const (
	TestsResourceDefaultTimeout = "20m"
)

var _ resource.ResourceWithConfigure = &TestsResource{}

func NewTestsResource() resource.Resource {
	return &TestsResource{WithTypeName: "tests"}
}

type TestsResource struct {
	framework.WithTypeName
	framework.WithNoOpDelete
	framework.WithNoOpRead

	repo  name.Repository
	ropts []remote.Option
}

type TestsResourceModel struct {
	Id      types.String               `tfsdk:"id"`
	Name    types.String               `tfsdk:"name"`
	Driver  DriverResourceModel        `tfsdk:"driver"`
	Drivers *TestsDriversResourceModel `tfsdk:"drivers"`
	Images  TestsImageResource         `tfsdk:"images"`
	Tests   []TestResourceModel        `tfsdk:"tests"`
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
				Default:     stringdefault.StaticString("imagetest"),
			},
			"driver": schema.StringAttribute{
				Description: "The driver to use for the test suite. Only one driver can be used at a time.",
				Required:    true,
			},
			"drivers": schema.SingleNestedAttribute{
				Description: "The resource specific driver configuration. This is merged with the provider scoped drivers configuration.",
				Optional:    true,
				Attributes: map[string]schema.Attribute{
					"k3s_in_docker": schema.SingleNestedAttribute{
						Description: "The k3s_in_docker driver",
						Optional:    true,
						Attributes: map[string]schema.Attribute{
							"cni": schema.BoolAttribute{
								Description: "Enable the CNI plugin",
								Optional:    true,
							},
							"network_policy": schema.BoolAttribute{
								Description: "Enable the network policy",
								Optional:    true,
							},
							"traefik": schema.BoolAttribute{
								Description: "Enable the traefik ingress controller",
								Optional:    true,
							},
							"metrics_server": schema.BoolAttribute{
								Description: "Enable the metrics server",
								Optional:    true,
							},
						},
					},
					"docker_in_docker": schema.SingleNestedAttribute{
						Description: "The docker_in_docker driver",
						Optional:    true,
						Attributes: map[string]schema.Attribute{
							"image_ref": schema.StringAttribute{
								Description: "The image reference to use for the docker-in-docker driver",
								Optional:    true,
							},
						},
					},
				},
			},
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
						"envs": schema.MapAttribute{
							Description: "Environment variables to set on the test container. These will overwrite the environment variables set in the image's config on conflicts.",
							Optional:    true,
							ElementType: types.StringType,
						},
					},
				},
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

	id := fmt.Sprintf("%s-%s", data.Name.ValueString(), uuid.New().String()[:4])
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

	for _, test := range data.Tests {
		l := l.With("test_name", test.Name.ValueString())
		l.InfoContext(ctx, "starting test", "driver", data.Driver)

		// Build the test image
		baseRepo, err := name.ParseReference(test.Image.ValueString())
		if err != nil {
			return []diag.Diagnostic{diag.NewErrorDiagnostic("failed to parse base image reference", err.Error())}
		}

		targetRepo, err := name.NewRepository(fmt.Sprintf("%s/%s", t.repo.String(), "imagetest"))
		if err != nil {
			return []diag.Diagnostic{diag.NewErrorDiagnostic("failed to create target repository", err.Error())}
		}

		layers := make([]bundler.Layerer, 0, len(test.Content))
		for _, c := range test.Content {
			target := c.Target.ValueString()
			if target == "" {
				target = "/imagetest"
			}

			layers = append(layers, bundler.NewFSLayerFromPath(c.Source.ValueString(), target))
		}

		l.InfoContext(ctx, "creating and publishing test image", "base_ref", baseRepo.String(), "target_ref", targetRepo.String())
		tref, err := bundler.Append(ctx, baseRepo, targetRepo,
			bundler.AppendWithLayers(layers...),
			bundler.AppendWithEnvs(test.Envs),
			bundler.AppendWithEnvs(map[string]string{
				"IMAGES": string(imgsResolvedData),
			}),
			bundler.AppendWithRemoteOptions(t.ropts...),
		)
		if err != nil {
			return []diag.Diagnostic{diag.NewErrorDiagnostic("failed to build test image", err.Error())}
		}

		l.InfoContext(ctx, "running test image", "test_ref", tref.String())
		if err := dr.Run(ctx, tref); err != nil {
			return []diag.Diagnostic{diag.NewErrorDiagnostic("failed to run test image", err.Error())}
		}
	}

	return
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
