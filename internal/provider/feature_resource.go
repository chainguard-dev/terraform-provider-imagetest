// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	itypes "github.com/chainguard-dev/terraform-provider-imagetest/internal/types"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

// Ensure provider defined types fully satisfy framework interfaces.
var (
	_ resource.Resource                = &FeatureResource{}
	_ resource.ResourceWithConfigure   = &FeatureResource{}
	_ resource.ResourceWithImportState = &FeatureResource{}
)

func NewFeatureResource() resource.Resource {
	return &FeatureResource{}
}

// FeatureResource defines the resource implementation.
type FeatureResource struct {
	id    string
	store *ProviderStore
}

// FeatureResourceModel describes the resource data model.
type FeatureResourceModel struct {
	Id          types.String     `tfsdk:"id"`
	Name        types.String     `tfsdk:"name"`
	Description types.String     `tfsdk:"description"`
	Assertions  []AssertionModel `tfsdk:"assert"`
	Setups      []AssertionModel `tfsdk:"setup"`
	Teardowns   []AssertionModel `tfsdk:"teardown"`
}

func (r *FeatureResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_feature"
}

func (r *FeatureResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		// This description is used by the documentation generator and the language server.
		MarkdownDescription: "Example resource",

		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true,
			},
			"name": schema.StringAttribute{
				Required: true,
			},
			"description": schema.StringAttribute{
				Optional: true,
			},
			"assert": schema.ListAttribute{
				ElementType: basetypes.ObjectType{
					AttrTypes: map[string]attr.Type{
						"cmd": basetypes.StringType{},
					},
				},
				Optional: true,
			},
			"setup": schema.ListAttribute{
				ElementType: basetypes.ObjectType{
					AttrTypes: map[string]attr.Type{
						"cmd": basetypes.StringType{},
					},
				},
				Optional: true,
			},
			"teardown": schema.ListAttribute{
				ElementType: basetypes.ObjectType{
					AttrTypes: map[string]attr.Type{
						"cmd": basetypes.StringType{},
					},
				},
				Optional: true,
			},
		},
	}
}

func (r *FeatureResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *FeatureResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data FeatureResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	data.Id = types.StringValue(r.id)

	// Save the feature into the store
	r.store.features.Set(r.id, data)

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *FeatureResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data FeatureResourceModel

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *FeatureResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data FeatureResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *FeatureResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data FeatureResourceModel

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}
}

func (r *FeatureResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

type AssertionModel struct {
	Cmd   types.String `tfsdk:"cmd"`
	level itypes.Level
}

func (a AssertionModel) Name() string {
	return "placeholder"
}

func (a AssertionModel) Command(ctx context.Context) string {
	return a.Cmd.ValueString()
}

func (a AssertionModel) WithLevel(level itypes.Level) AssertionModel {
	a.level = level
	return a
}

func (a AssertionModel) Level() itypes.Level {
	return a.level
}
