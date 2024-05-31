package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/containers/provider"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/inventory"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/log"
	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	kresource "k8s.io/apimachinery/pkg/api/resource"
)

const (
	defaultHarnessCreateTimeout = 5 * time.Minute
)

// HarnessResource provides common methods for all HarnessResource
// implementations.
type HarnessResource struct {
	store *ProviderStore
}

// FeatureHarnessResourceModel is the common data model all harnesses output to
// be passed into dependent features.
type FeatureHarnessResourceModel struct {
	Id        types.String             `tfsdk:"id"`
	Name      types.String             `tfsdk:"name"`
	Inventory InventoryDataSourceModel `tfsdk:"inventory"`
	Skipped   types.Bool               `tfsdk:"skipped"`
}

type FeatureHarnessVolumeMountModel struct {
	Source      ContainerVolumeResourceModel `tfsdk:"source"`
	Destination string                       `tfsdk:"destination"`
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

type ContainerResources struct {
	Memory *ContainerMemoryResources `tfsdk:"memory"`
	Cpu    *ContainerCpuResources    `tfsdk:"cpu"`
}

type ContainerMemoryResources struct {
	Request types.String `tfsdk:"request"`
	Limit   types.String `tfsdk:"limit"`
}

type ContainerCpuResources struct {
	Request types.String `tfsdk:"request"`
	Limit   types.String `tfsdk:"limit"`
}

func (r *HarnessResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ModifyPlan adds the harness to the inventory during both the plan and apply
// phase. This uses the more verbose GetAttribute() instead of Get() because
// terraform-plugin-framework does not support embedding models without nesting.
func (r *HarnessResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if !req.State.Raw.IsNull() {
		// TODO: This currently exists to handle `terraform destroy` which occurs
		// during acceptance testing. In the future, we should properly handle any
		// pre-existing state
		return
	}

	inv := InventoryDataSourceModel{}
	if diags := req.Config.GetAttribute(ctx, path.Root("inventory"), &inv); diags.HasError() {
		return
	}

	var name string
	if diags := req.Config.GetAttribute(ctx, path.Root("name"), &name); diags.HasError() {
		return
	}

	// The ID is the {name}-{inventory-hash}. It's intentionally chose to be more
	// user-friendly than just a hash, since it is prepended to resources the
	// harnesses will create.
	invEnc, err := r.store.Encode(inv.Seed.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("failed to add harness", "encoding harness id")
		return
	}

	id := fmt.Sprintf("%s-%s", name, invEnc)

	if diag := resp.Plan.SetAttribute(ctx, path.Root("id"), id); diag.HasError() {
		return
	}

	added, err := r.store.Inventory(inv).AddHarness(ctx, inventory.Harness(id))
	if err != nil {
		resp.Diagnostics.AddError("failed to add harness", err.Error())
	}

	if added {
		log.Debug(ctx, fmt.Sprintf("Harness.ModifyPlan() | harness [%s] added to inventory", id))
	}
}

func (r *HarnessResource) ShouldSkip(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) bool {
	inv := InventoryDataSourceModel{}
	if diags := req.Config.GetAttribute(ctx, path.Root("inventory"), &inv); diags.HasError() {
		return false
	}

	var id string
	if diags := req.Plan.GetAttribute(ctx, path.Root("id"), &id); diags.HasError() {
		return false
	}

	feats, err := r.store.Inventory(inv).GetFeatures(ctx, inventory.Harness(id))
	if err != nil {
		resp.Diagnostics.AddError("failed to get features from harness", err.Error())
		return false
	}

	// skipping is only possible when labels are specified
	if len(r.store.labels) == 0 {
		return false
	}

	skip := false
	for _, feat := range feats {
		for pk, pv := range r.store.labels {
			fv, ok := feat.Labels[pk]
			if ok && (fv != pv) {
				// if the feature label exists but the value doesn't match, skip
				skip = true
				break
			}
		}
	}

	if skip {
		resp.Diagnostics.AddWarning(
			fmt.Sprintf("skipping harness [%s] creation", id),
			"given provider runtime labels do not match feature labels")
	}

	return skip
}

// ParseResources parses the ContainerResources object into a provider.ContainerResourcesRequest object.
func ParseResources(resources *ContainerResources) (provider.ContainerResourcesRequest, error) {
	req := provider.ContainerResourcesRequest{}

	if resources == nil {
		return req, nil
	}

	if resources.Memory != nil {
		if resources.Memory.Request.ValueString() != "" {
			q, err := kresource.ParseQuantity(resources.Memory.Request.ValueString())
			if err != nil {
				return req, fmt.Errorf("failed to parse memory request: %w", err)
			}
			req.MemoryRequest = q
		}

		if resources.Memory.Limit.ValueString() != "" {
			q, err := kresource.ParseQuantity(resources.Memory.Limit.ValueString())
			if err != nil {
				return req, fmt.Errorf("failed to parse memory limit: %w", err)
			}
			req.MemoryLimit = q
		}
	}

	if resources.Cpu != nil {
		if resources.Cpu.Request.ValueString() != "" {
			q, err := kresource.ParseQuantity(resources.Cpu.Request.ValueString())
			if err != nil {
				return req, fmt.Errorf("failed to parse cpu request: %w", err)
			}
			req.CpuRequest = q
		}

		if resources.Cpu.Limit.ValueString() != "" {
			q, err := kresource.ParseQuantity(resources.Cpu.Limit.ValueString())
			if err != nil {
				return req, fmt.Errorf("failed to parse cpu limit: %w", err)
			}
			req.CpuLimit = q
		}
	}

	return req, nil
}

// Base implementation for read.
func baseRead(ctx context.Context, data interface{}, req resource.ReadRequest, resp *resource.ReadResponse) {
	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, data)...)
}

// Base implementation for update.
func baseUpdate(ctx context.Context, data interface{}, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, data)...)
}

// Base implementation for delete.
func baseDelete(ctx context.Context, data interface{}, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, data)...)

	if resp.Diagnostics.HasError() {
		return
	}
}

// AddHarnessSchemaAttributes adds common attributes to the given map. values
// provided in attrs will override any specified defaults.
func addHarnessResourceSchemaAttributes(ctx context.Context) map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"id": schema.StringAttribute{
			Description: "The unique identifier for the harness. This is generated from the inventory seed and harness name.",
			Computed:    true,
		},
		"name": schema.StringAttribute{
			Description: "The name of the harness. This must be unique within the scope of the provided inventory.",
			Required:    true,
		},
		"inventory": schema.SingleNestedAttribute{
			Description: "The inventory this harness belongs to. This is received as a direct input from a data.imagetest_inventory data source.",
			Required:    true,
			Attributes: map[string]schema.Attribute{
				"seed": schema.StringAttribute{
					Required: true,
				},
			},
		},
		"skipped": schema.BoolAttribute{
			Description: "Whether or not to skip creating the harness based on runtime inputs and the dependent features within this inventory.",
			Computed:    true,
		},
		"timeouts": timeouts.Attributes(ctx, timeouts.Opts{
			Create:            true,
			CreateDescription: "The maximum time to wait for the k3s harness to be created.",
		}),
	}
}

func defaultFeatureHarnessResourceSchemaAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"harness": schema.SingleNestedAttribute{
			Required: true,
			Attributes: map[string]schema.Attribute{
				"id": schema.StringAttribute{
					Required: true,
				},
				"name": schema.StringAttribute{
					Required: true,
				},
				"skipped": schema.BoolAttribute{
					Required: true,
				},
				"inventory": schema.SingleNestedAttribute{
					Required: true,
					Attributes: map[string]schema.Attribute{
						"seed": schema.StringAttribute{
							Required: true,
						},
					},
				},
			},
		},
	}
}
