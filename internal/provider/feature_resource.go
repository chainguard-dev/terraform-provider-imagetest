package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/environment"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/features"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harnesses"
	itypes "github.com/chainguard-dev/terraform-provider-imagetest/internal/types"
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
	store *ProviderStore
	id    string
}

// FeatureResourceModel describes the resource data model.
type FeatureResourceModel struct {
	Id          types.String       `tfsdk:"id"`
	Name        types.String       `tfsdk:"name"`
	Description types.String       `tfsdk:"description"`
	HarnessId   types.String       `tfsdk:"harness"`
	Labels      types.Map          `tfsdk:"labels"`
	Before      []FeatureStepModel `tfsdk:"before"`
	After       []FeatureStepModel `tfsdk:"after"`
	Steps       []FeatureStepModel `tfsdk:"steps"`
}

type FeatureStepModel struct {
	Name types.String `tfsdk:"name"`
	Cmd  types.String `tfsdk:"cmd"`
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
				Description: "The name of the feature",
				Required:    true,
			},
			"description": schema.StringAttribute{
				Description: "A descriptor of the feature",
				Optional:    true,
			},
			"harness": schema.StringAttribute{
				Description: "The ID of the test harness to use for the feature",
				Optional:    true,
			},
			"before": schema.ListNestedAttribute{
				Description: "Actions to run against the harness before the core feature steps.",
				Optional:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Optional: true,
						},
						"cmd": schema.StringAttribute{
							Required: true,
						},
					},
				},
			},
			"after": schema.ListNestedAttribute{
				Description: "Actions to run againast the harness after the core steps have run OR after a step has failed.",
				Optional:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Optional: true,
						},
						"cmd": schema.StringAttribute{
							Required: true,
						},
					},
				},
			},
			"steps": schema.ListNestedAttribute{
				Description: "Actions to run against the harness.",
				Optional:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Optional: true,
						},
						"cmd": schema.StringAttribute{
							Required: true,
						},
					},
				},
			},
			"labels": schema.MapAttribute{
				Description: "A set of labels used to optionally filter execution of the feature",
				Optional:    true,
				ElementType: basetypes.StringType{},
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

	labels := make(map[string]string)
	if diags := data.Labels.ElementsAs(ctx, &labels, false); diags.HasError() {
		resp.Diagnostics.AddError("failed to convert labels", "...")
		return
	}

	var harness itypes.Harness
	// Use a host harness if none is specified
	if data.HarnessId.IsUnknown() || data.HarnessId.IsNull() {
		harness = harnesses.NewHost()
	} else {
		// Get the harness from the store
		h, ok := r.store.harnesses.Get(data.HarnessId.ValueString())
		if !ok {
			resp.Diagnostics.AddError("invalid harness id", "...")
			return
		}
		harness = h
	}

	builder := features.NewBuilder(data.Name.ValueString()).
		WithDescription(data.Description.ValueString()).
		WithLabels(labels)

		// Add the harness as the first before, and the last after

	builder = builder.WithBefore("HarnessSetup", harness.Setup())
	for _, before := range data.Before {
		builder = builder.WithBefore(before.Name.ValueString(), harness.StepFn(before.Cmd.ValueString()))
	}

	for _, step := range data.Steps {
		builder = builder.WithAssessment(step.Name.ValueString(), harness.StepFn(step.Cmd.ValueString()))
	}

	for _, after := range data.After {
		builder = builder.WithAfter(after.Name.ValueString(), harness.StepFn(after.Cmd.ValueString()))
	}
	builder = builder.WithAfter("HarnessFinish", harness.Finish())

	if err := r.store.env.Test(ctx, builder.Build()); err != nil {
		var skipped *environment.ErrTestSkipped
		if errors.As(err, &skipped) {
			resp.Diagnostics.AddWarning(
				"Skipping testing feature not matching environment labels", fmt.Sprintf("Feature: %s\n\n(feature) %q\n(runtime) %q", skipped.FeatureName, skipped.FeatureLabels, skipped.CheckedLabels))
			if _, err := harness.Finish()(ctx); err != nil {
				resp.Diagnostics.AddError("failed to finish harness after skipping test", err.Error())
				return
			}
		} else {
			resp.Diagnostics.AddError("failed to test feature", err.Error())
			return
		}
	}

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
