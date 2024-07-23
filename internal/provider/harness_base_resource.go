package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/containers/provider"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/inventory"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/log"
	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	kresource "k8s.io/apimachinery/pkg/api/resource"
)

const (
	defaultHarnessCreateTimeout = 5 * time.Minute
)

// BaseHarnessResource provides common methods for all BaseHarnessResource
// implementations.
type BaseHarnessResource struct {
	store *ProviderStore
}

type BaseHarnessResourceModel struct {
	Id        types.String             `tfsdk:"id"`
	Name      types.String             `tfsdk:"name"`
	Inventory InventoryDataSourceModel `tfsdk:"inventory"`
	Timeouts  timeouts.Value           `tfsdk:"timeouts"`
}

// FeatureHarnessResourceModel is the common data model all harnesses output to
// be passed into dependent features.
type FeatureHarnessResourceModel struct {
	Id        types.String             `tfsdk:"id"`
	Name      types.String             `tfsdk:"name"`
	Inventory InventoryDataSourceModel `tfsdk:"inventory"`
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

type HarnessHooksModel struct {
	PreStart  types.List `tfsdk:"pre_start"`
	PostStart types.List `tfsdk:"post_start"`
}

func (r *BaseHarnessResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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
func (r *BaseHarnessResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	// If we have state, and the plan for id is null, we're in a destroy so do nothing
	if !req.State.Raw.IsNull() && req.Plan.Raw.IsNull() {
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

	// Set the "constants" we know during plan
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("id"), id)...)
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("inventory"), inv)...)
	if resp.Diagnostics.HasError() {
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

// Read implements resource.Resource. This is intentionally a no-op.
func (r *BaseHarnessResource) Read(context.Context, resource.ReadRequest, *resource.ReadResponse) {}

func (r *BaseHarnessResource) create(ctx context.Context, req resource.CreateRequest, harness harness.Harness) diag.Diagnostics {
	// We unmarshal this explicitly because of the insanity of the framework not
	// supporting embedded structs
	var data BaseHarnessResourceModel
	req.Plan.GetAttribute(ctx, path.Root("inventory"), &data.Inventory)
	req.Plan.GetAttribute(ctx, path.Root("name"), &data.Name)
	req.Plan.GetAttribute(ctx, path.Root("id"), &data.Id)
	req.Plan.GetAttribute(ctx, path.Root("timeouts"), &data.Timeouts)

	return r.do(ctx, data, harness)
}

func (r *BaseHarnessResource) update(ctx context.Context, req resource.UpdateRequest, harness harness.Harness) diag.Diagnostics {
	// We unmarshal this explicitly because of the insanity of the framework not
	// supporting embedded structs
	var data BaseHarnessResourceModel
	req.Plan.GetAttribute(ctx, path.Root("inventory"), &data.Inventory)
	req.Plan.GetAttribute(ctx, path.Root("name"), &data.Name)
	req.Plan.GetAttribute(ctx, path.Root("id"), &data.Id)
	req.Plan.GetAttribute(ctx, path.Root("timeouts"), &data.Timeouts)

	return r.do(ctx, data, harness)
}

func (r *BaseHarnessResource) do(ctx context.Context, data BaseHarnessResourceModel, harness harness.Harness) diag.Diagnostics {
	diags := make(diag.Diagnostics, 0)

	if r.skip(ctx, data.Inventory, data.Id.ValueString()) {
		return append(diags, diag.NewWarningDiagnostic(
			"skipping harness",
			fmt.Sprintf("id [%s] reason: feature labels do not match", data.Id.ValueString()),
		))
	}

	// NOTE: This is technically different for create/update, but we reuse the
	// create timeouts everywhere
	timeout, diags := data.Timeouts.Create(ctx, defaultHarnessCreateTimeout)
	if diags.HasError() {
		log.Warn(ctx, fmt.Sprintf("failed to parse harness create timeout, using the default timeout of %s", defaultHarnessCreateTimeout), "error", diags)
		diags = append(diags, diags...)
		timeout = defaultHarnessCreateTimeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	r.store.AddHarness(data.Id.ValueString(), harness)

	ctx, err := r.store.Logger(ctx, data.Inventory, "harness_id", data.Id.ValueString(), "harness_name", data.Name.ValueString())
	if err != nil {
		return []diag.Diagnostic{diag.NewErrorDiagnostic("failed to initialize logger(s)", err.Error())}
	}

	if err := harness.Create(ctx); err != nil {
		return []diag.Diagnostic{diag.NewErrorDiagnostic("failed to create harness", err.Error())}
	}

	return diags
}

func (r *BaseHarnessResource) skip(ctx context.Context, inv InventoryDataSourceModel, harnessId string) bool {
	feats, err := r.store.Inventory(inv).GetFeatures(ctx, inventory.Harness(harnessId))
	if err != nil {
		return false
	}

	// skipping is only possible when labels are specified
	if len(r.store.labels) == 0 {
		return false
	}

	for _, feat := range feats {
		for pk, pv := range r.store.labels {
			fv, ok := feat.Labels[pk]
			if ok && (fv != pv) {
				// if the feature label exists but the value doesn't match, skip
				return true
			}
		}
	}

	return false
}

// Delete implements resource.Resource. This is intentionally a no-op.
func (r *BaseHarnessResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
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

func (r *BaseHarnessResource) schemaAttributes(ctx context.Context) map[string]schema.Attribute {
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
