package provider

import (
	"context"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harnesses/k3s"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
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
	Id                   types.String                             `tfsdk:"id"`
	Image                types.String                             `tfsdk:"image"`
	DisableCni           types.Bool                               `tfsdk:"disable_cni"`
	DisableTraefik       types.Bool                               `tfsdk:"disable_traefik"`
	DisableMetricsServer types.Bool                               `tfsdk:"disable_metrics_server"`
	Registries           map[string]RegistryResourceModel         `tfsdk:"registries"`
	Networks             map[string]ContainerResourceModelNetwork `tfsdk:"networks"`
}

type RegistryResourceModel struct {
	Auth   *RegistryResourceAuthModel   `tfsdk:"auth"`
	Tls    *RegistryResourceTlsModel    `tfsdk:"tls"`
	Mirror *RegistryResourceMirrorModel `tfsdk:"mirror"`
}

type RegistryResourceAuthModel struct {
	Username types.String `tfsdk:"username"`
	Password types.String `tfsdk:"password"`
	Auth     types.String `tfsdk:"auth"`
}

type RegistryResourceTlsModel struct {
	CertFile types.String `tfsdk:"cert_file"`
	KeyFile  types.String `tfsdk:"key_file"`
	CaFile   types.String `tfsdk:"ca_file"`
}

type RegistryResourceMirrorModel struct {
	Endpoints types.List `tfsdk:"endpoints"`
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

	kopts := []k3s.Option{
		k3s.WithImage(data.Image.ValueString()),
	}

	registries := make(map[string]RegistryResourceModel)
	if data.Registries != nil {
		registries = data.Registries
	}

	networks := []string{}
	if data.Networks != nil {
		for _, v := range data.Networks {
			networks = append(networks, v.Name.ValueString())
		}
	}

	if r.store.providerResourceData.Harnesses != nil {
		if pc := r.store.providerResourceData.Harnesses.K3s; pc != nil {
			for k, v := range pc.Registries {
				registries[k] = v
			}

			for _, v := range pc.Networks {
				networks = append(networks, v.Name.ValueString())
			}
		}
	}

	for rname, rdata := range registries {
		if rdata.Auth != nil {
			if rdata.Auth.Auth.IsNull() && rdata.Auth.Password.IsNull() && rdata.Auth.Username.IsNull() {
				kopts = append(kopts, k3s.WithAuthFromKeychain(rname))
			} else {
				kopts = append(kopts, k3s.WithAuthFromStatic(rname, rdata.Auth.Username.ValueString(), rdata.Auth.Password.ValueString(), rdata.Auth.Auth.ValueString()))
			}
		}

		if rdata.Mirror != nil {
			endpoints := []string{}
			if diags := rdata.Mirror.Endpoints.ElementsAs(ctx, &endpoints, false); diags.HasError() {
				resp.Diagnostics.AddError("failed to convert mirror endpoints", "...")
				return
			}
			kopts = append(kopts, k3s.WithRegistryMirror(rname, endpoints...))
		}
	}

	kopts = append(kopts, k3s.WithNetworks(networks...))

	harness, err := k3s.New(r.id, kopts...)
	if err != nil {
		resp.Diagnostics.AddError("failed to initialize k3s harness", err.Error())
		return
	}

	r.store.harnesses.Set(r.id, harness)

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
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
