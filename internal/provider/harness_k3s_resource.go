package provider

import (
	"context"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harnesses"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure provider defined types fully satisfy framework interfaces.
var (
	_ resource.Resource                = &HarnessK3sResource{}
	_ resource.ResourceWithConfigure   = &HarnessK3sResource{}
	_ resource.ResourceWithImportState = &HarnessK3sResource{}
)

func NewHarnessK3sResource() resource.Resource {
	return &HarnessK3sResource{}
}

// HarnessK3sResource defines the resource implementation.
type HarnessK3sResource struct {
	id    string
	store *ProviderStore
}

// HarnessK3sResourceModel describes the resource data model.
type HarnessK3sResourceModel struct {
	Id                   types.String `tfsdk:"id"`
	Image                types.String `tfsdk:"image"`
	DisableCni           types.Bool   `tfsdk:"disable_cni"`
	DisableTraefik       types.Bool   `tfsdk:"disable_traefik"`
	DisableMetricsServer types.Bool   `tfsdk:"disable_metrics_server"`
}

func (r *HarnessK3sResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_harness_k3s"
}

func (r *HarnessK3sResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `A harness that runs steps in a sandbox container networked to a running k3s cluster.`,

		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true,
			},
			"disable_cni": schema.BoolAttribute{
				Description: "When true, the builtin (flannel) CNI will be disabled.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
			},
			"disable_traefik": schema.BoolAttribute{
				Description: "When true, the builtin traefik ingress controller will be disabled.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
			},
			"disable_metrics_server": schema.BoolAttribute{
				Description: "When true, the builtin metrics server will be disabled.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
			},
			"image": schema.StringAttribute{
				Description: "The full image reference to use for the k3s container.",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("cgr.dev/chainguard/k3s:latest"),
			},
		},
	}
}

func (r *HarnessK3sResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	// Prevent panic if the provider has not been configured.
	if req.ProviderData == nil {
		return
	}

	store, ok := req.ProviderData.(*ProviderStore)
	if !ok {
		resp.Diagnostics.AddError("invalid provider data", "...")
		return
	}

	r.id = store.RandomID()
	r.store = store
}

func (r *HarnessK3sResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data HarnessK3sResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	data.Id = types.StringValue(r.id)

	harness, err := harnesses.NewK3s(r.id, r.k3sConfig(ctx, data))
	if err != nil {
		resp.Diagnostics.AddError("failed to initialize k3s harness", err.Error())
		return
	}

	r.store.harnesses.Set(r.id, harness)

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *HarnessK3sResource) k3sConfig(ctx context.Context, data HarnessK3sResourceModel) harnesses.K3sConfig {
	return harnesses.K3sConfig{
		Image:         data.Image.ValueString(),
		Cni:           !data.DisableCni.ValueBool(),
		MetricsServer: !data.DisableMetricsServer.ValueBool(),
		Traefik:       !data.DisableTraefik.ValueBool(),
	}
}

func (r *HarnessK3sResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data HarnessK3sResourceModel

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *HarnessK3sResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data HarnessK3sResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *HarnessK3sResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data HarnessK3sResourceModel

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}
}

func (r *HarnessK3sResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
