package provider

import (
	"context"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harnesses"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure provider defined types fully satisfy framework interfaces.
var (
	_ resource.Resource                = &HarnessK3sResource{}
	_ resource.ResourceWithConfigure   = &HarnessK3sResource{}
	_ resource.ResourceWithImportState = &HarnessK3sResource{}
)

func NewHarnessK3sResource() resource.Resource {
	return &HarnessK3sResource{
		HarnessNullResource: &HarnessNullResource{},
	}
}

// HarnessK3sResource defines the resource implementation.
type HarnessK3sResource struct {
	*HarnessNullResource
}

// HarnessK3sResourceModel describes the resource data model.
type HarnessK3sResourceModel struct {
	Id                   types.String `tfsdk:"id"`
	DisableCni           types.Bool   `tfsdk:"disable_cni"`
	DisableTraefik       types.Bool   `tfsdk:"disable_traefik"`
	DisableMetricsServer types.Bool   `tfsdk:"disable_metrics_server"`
}

func (r *HarnessK3sResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_harness_k3s"
}

func (r *HarnessK3sResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		// This description is used by the documentation generator and the language server.
		MarkdownDescription: "Example resource",

		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Example identifier",
			},
			"disable_cni": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(false),
			},
			"disable_traefik": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(true),
			},
			"disable_metrics_server": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(true),
			},
		},
	}
}

func (r *HarnessK3sResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data HarnessK3sResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	data.Id = types.StringValue(r.id)

	kcfg := harnesses.DefaultK3sConfig()
	kcfg.DisableCni = data.DisableCni.ValueBool()
	kcfg.DisableTraefik = data.DisableTraefik.ValueBool()
	kcfg.DisableMetricsServer = data.DisableMetricsServer.ValueBool()

	harness := harnesses.NewK3s(r.id, r.store.ports, kcfg)

	// Store harness in the provider store
	r.store.harnesses.Set(r.id, harness)

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
