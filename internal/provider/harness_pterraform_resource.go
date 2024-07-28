package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness/pterraform"
	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.ResourceWithModifyPlan = &HarnessPterraformResource{}

func NewHarnessPterraformResource() resource.Resource {
	return &HarnessPterraformResource{}
}

type HarnessPterraformResource struct {
	BaseHarnessResource
}

type HarnessPterraformResourceModel struct {
	Id        types.String             `tfsdk:"id"`
	Name      types.String             `tfsdk:"name"`
	Inventory InventoryDataSourceModel `tfsdk:"inventory"`
	Timeouts  timeouts.Value           `tfsdk:"timeouts"`

	Path types.String `tfsdk:"path"`
	Vars types.String `tfsdk:"vars"`
}

func (r *HarnessPterraformResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_harness_pterraform"
}

func (r *HarnessPterraformResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data HarnessPterraformResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)

	harness, diags := r.harness(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if diags.HasError() {
		return
	}

	resp.Diagnostics.Append(r.create(ctx, req, harness)...)
	if resp.Diagnostics.HasError() {
		return
	}
}

func (r *HarnessPterraformResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data HarnessPterraformResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)

	harness, diags := r.harness(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if diags.HasError() {
		return
	}

	resp.Diagnostics.Append(r.update(ctx, req, harness)...)
	if resp.Diagnostics.HasError() {
		return
	}
}

func (r *HarnessPterraformResource) harness(_ context.Context, data *HarnessPterraformResourceModel) (harness.Harness, diag.Diagnostics) {
	diags := make(diag.Diagnostics, 0)

	popts := []pterraform.Option{}

	// Ensure the source path exists
	if _, err := os.Stat(data.Path.ValueString()); err != nil {
		diags = append(diags, diag.NewErrorDiagnostic(
			fmt.Sprintf("failed to find source path %[1]s", data.Path.ValueString()),
			fmt.Sprintf(`The source path %[1]s does not exist`, data.Path.ValueString()),
		))
		return nil, diags
	}

	ws, ok := os.LookupEnv("IMAGETEST_WORKSPACE")
	if ok {
		p := path.Join(ws, data.Name.ValueString())
		diags = append(diags, diag.NewWarningDiagnostic(
			fmt.Sprintf("Using pterraform harness in dev mode. The working directory will be persisted across runs to `%[1]s`, and you will be responsible for cleaning up the workspace.", p),
			fmt.Sprintf(`You have used IMAGETEST_WORKSPACE=%[1]s to activate the pterraform harness in dev mode.

This will only work as intended if all harnesses are named uniquely across ALL inventories.
		`, ws)))
		popts = append(popts, pterraform.WithWorkspace(ws))
	}

	if data.Vars.ValueString() != "" {
		var vars json.RawMessage

		if err := json.NewDecoder(strings.NewReader(data.Vars.ValueString())).Decode(&vars); err != nil {
			return nil, []diag.Diagnostic{diag.NewErrorDiagnostic("failed to unmarshal vars as json", err.Error())}
		}
		popts = append(popts, pterraform.WithVars(vars))
	}

	harness, err := pterraform.New(
		os.DirFS(data.Path.ValueString()),
		popts...,
	)
	if err != nil {
		return nil, []diag.Diagnostic{diag.NewErrorDiagnostic("failed to create pterraform harness", err.Error())}
	}

	return harness, diags
}

func (r *HarnessPterraformResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `A harness created from a generic terraform invocation.`,
		Attributes: mergeResourceSchemas(
			r.schemaAttributes(ctx),
			map[string]schema.Attribute{
				"path": schema.StringAttribute{
					Description: "The path to the terraform source directory.",
					Required:    true,
				},
				"vars": schema.StringAttribute{
					Description: "A json encoded string of variables to pass to the terraform invocation. This will be passed in as a .tfvars.json var file.",
					Optional:    true,
				},
			},
		),
	}
}
