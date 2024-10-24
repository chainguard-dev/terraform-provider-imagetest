package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/bundler"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness/k3s"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/provider/framework"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const (
	// TODO: Make the default feature timeout configurable?
	defaultTestSuiteCreateTimeout = 15 * time.Minute
)

var _ resource.Resource = &TestSuiteResource{}

func NewTestSuiteResource() resource.Resource {
	return &TestSuiteResource{WithTypeName: "test_suite"}
}

// TestSuiteResource defines the resource implementation.
type TestSuiteResource struct {
	framework.WithTypeName
	framework.WithNoOpRead
	framework.WithNoOpDelete

	store *ProviderStore
}

// TestSuiteResourceModel describes the resource data model.
type TestSuiteResourceModel struct {
	Name        types.String                 `tfsdk:"name"`
	Description types.String                 `tfsdk:"description"`
	Timeouts    timeouts.Value               `tfsdk:"timeouts"`
	Tests       []TestSuiteTestResourceModel `tfsdk:"tests"`
	Labels      map[string]string            `tfsdk:"labels"`
	Images      map[string]string            `tfsdk:"images"`
}

type TestSuiteTestResourceModel struct {
	Name      types.String                      `tfsdk:"name"`
	BaseImage types.String                      `tfsdk:"base_image"`
	Sources   []TestSuiteTestLayerResourceModel `tfsdk:"sources"`
	TestRef   types.String                      `tfsdk:"test_ref"`
	Status    types.String                      `tfsdk:"status"`
	Labels    map[string]string                 `tfsdk:"labels"`
}

type TestSuiteTestLayerResourceModel struct {
	Source types.String `tfsdk:"source"`
	Target types.String `tfsdk:"target"`
}

func (r *TestSuiteResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "",
		Attributes: mergeResourceSchemas(
			map[string]schema.Attribute{
				"name": schema.StringAttribute{
					Description: "The name of the test suite",
					Required:    true,
				},
				"description": schema.StringAttribute{
					Description: "A description of the test suite.",
					Optional:    true,
				},
				"timeouts": timeouts.Attributes(ctx, timeouts.Opts{
					Create: true,
				}),
				"images": schema.MapAttribute{
					ElementType: types.StringType,
					Optional:    true,
					Description: "Images to use for the test suite.",
				},
				"labels": schema.MapAttribute{
					ElementType: types.StringType,
					Optional:    true,
					Description: "Labels to apply to the test suite.",
				},
				"tests": schema.ListNestedAttribute{
					Description: "A list of tests to run.",
					Optional:    true,
					NestedObject: schema.NestedAttributeObject{
						Attributes: map[string]schema.Attribute{
							"name": schema.StringAttribute{
								Description: "The name of the test",
								Required:    true,
							},
							"base_image": schema.StringAttribute{
								Description: "The base image to use for the test.",
								Required:    true,
							},
							"sources": schema.ListNestedAttribute{
								Description: "A list of sources (directories) to add to the test container.",
								Optional:    true,
								NestedObject: schema.NestedAttributeObject{
									Attributes: map[string]schema.Attribute{
										"source": schema.StringAttribute{
											Description: "The relative or absolute path on the host to the directory to include.",
											Required:    true,
										},
										"target": schema.StringAttribute{
											Description: "The absolute path on the container to place the directory.",
											Required:    true,
										},
									},
								},
							},
							"labels": schema.MapAttribute{
								ElementType: types.StringType,
								Optional:    true,
								Description: "Labels to apply to the test image.",
							},
							"test_ref": schema.StringAttribute{
								Description: "The reference to the dynamically created test image.",
								Computed:    true,
							},
							"status": schema.StringAttribute{
								Description: "The status of the test.",
								Computed:    true,
							},
						},
					},
				},
			},
		),
	}
}

func (r *TestSuiteResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	// Prevent panic if the provider has not been configured.
	if req.ProviderData == nil {
		return
	}

	store, ok := req.ProviderData.(*ProviderStore)
	if !ok {
		resp.Diagnostics.AddError("invalid provider data", "...")
		return
	}

	r.store = store
}

func (r *TestSuiteResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data TestSuiteResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(r.do(ctx, &data)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
}

func (r *TestSuiteResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data TestSuiteResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(r.do(ctx, &data)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
}

func (r *TestSuiteResource) do(ctx context.Context, data *TestSuiteResourceModel) (ds diag.Diagnostics) {
	timeout, diags := data.Timeouts.Create(ctx, defaultTestSuiteCreateTimeout)
	if diags.HasError() {
		ds.Append(diags...)
		return ds
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	h, err := k3s.New(
		k3s.WithAuthFromKeychain(r.store.repo.RegistryStr()),
	)
	if err != nil {
		return []diag.Diagnostic{diag.NewErrorDiagnostic("failed to create harness", err.Error())}
	}

	if err := h.Create(ctx); err != nil {
		return []diag.Diagnostic{diag.NewErrorDiagnostic("failed to start harness", err.Error())}
	}

	// defer func() {
	// 	if err := h.Destroy(ctx); err != nil {
	// 		ds.AddError("failed to destroy harness", err.Error())
	// 	}
	// }()

	pimgs := map[string]struct {
		Registry     string `json:"registry"`
		Repo         string `json:"repo"`
		RegistryRepo string `json:"registry_repo"`
		Digest       string `json:"digest"`
		PseudoTag    string `json:"pseudo_tag"`
		Ref          string `json:"ref"`
	}{}

	for k, v := range data.Images {
		p, err := name.ParseReference(v)
		if err != nil {
			return []diag.Diagnostic{diag.NewErrorDiagnostic("invalid image reference", err.Error())}
		}

		if _, ok := p.(name.Tag); ok {
			return []diag.Diagnostic{diag.NewErrorDiagnostic("reference contains only a tag, but a digest is required", "")}
		}

		pimgs[k] = struct {
			Registry     string `json:"registry"`
			Repo         string `json:"repo"`
			RegistryRepo string `json:"registry_repo"`
			Digest       string `json:"digest"`
			PseudoTag    string `json:"pseudo_tag"`
			Ref          string `json:"ref"`
		}{
			Registry:     p.Context().RegistryStr(),
			Repo:         p.Context().RepositoryStr(),
			RegistryRepo: p.Context().RegistryStr() + "/" + p.Context().RepositoryStr(),
			Digest:       p.Identifier(),
			PseudoTag:    fmt.Sprintf("unused@%s", p.Identifier()),
			Ref:          p.String(),
		}
	}

	// serialize the images to json
	envs, err := json.Marshal(pimgs)
	if err != nil {
		return []diag.Diagnostic{diag.NewErrorDiagnostic("failed to serialize images", err.Error())}
	}

	for i, test := range data.Tests {
		data.Tests[i].Status = types.StringValue("failed")

		bref, err := name.ParseReference(test.BaseImage.ValueString())
		if err != nil {
			return []diag.Diagnostic{diag.NewErrorDiagnostic("invalid base image reference", err.Error())}
		}

		b, err := bundler.NewAppender(bref,
			bundler.AppenderWithRemoteOptions(r.store.ropts...),
			bundler.AppenderWithEnvs(map[string]string{
				"IMAGES": string(envs),
			}),
		)
		if err != nil {
			return []diag.Diagnostic{diag.NewErrorDiagnostic("failed to create bundler", err.Error())}
		}

		ls := []bundler.Layerer{}
		for _, layer := range test.Sources {
			ls = append(ls, bundler.NewFSLayer(
				os.DirFS(layer.Source.ValueString()),
				layer.Target.ValueString(),
			))
		}

		tref, err := b.Bundle(ctx, r.store.repo, ls...)
		if err != nil {
			return []diag.Diagnostic{diag.NewErrorDiagnostic("failed to bundle image", err.Error())}
		}

		data.Tests[i].TestRef = types.StringValue(tref.String())

		if err := h.Run(ctx, tref); err != nil {
			return []diag.Diagnostic{diag.NewErrorDiagnostic("failed to run test", err.Error())}
		}

		data.Tests[i].Status = types.StringValue("passed")
	}

	return ds
}
