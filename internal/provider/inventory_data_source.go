package provider

import (
	"context"
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure provider defined types fully satisfy framework interfaces.
var (
	_ datasource.DataSource = &InventoryDataSource{}
)

func NewInventoryDataSource() datasource.DataSource {
	return &InventoryDataSource{}
}

// InventoryDataSource defines the data source implementation.
type InventoryDataSource struct {
	store *ProviderStore
}

// InventoryDataSourceModel describes the data source data model.
type InventoryDataSourceModel struct {
	Id   types.String `tfsdk:"id"`
	Seed types.String `tfsdk:"seed"`
}

func (d *InventoryDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_inventory"
}

func (d *InventoryDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		// This description is used by the documentation generator and the language server.
		MarkdownDescription: "Inventory data source. Keeps track of harness resources.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true,
			},
			"seed": schema.StringAttribute{
				Computed: true,
			},
		},
	}
}

func (d *InventoryDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	// Prevent panic if the provider has not been configured.
	if req.ProviderData == nil {
		return
	}

	store, ok := req.ProviderData.(*ProviderStore)
	if !ok {
		resp.Diagnostics.AddError("invalid provider data", "...")
		return
	}

	d.store = store
}

func (d *InventoryDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data InventoryDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	newUUID, err := uuid.NewUUID()
	if err != nil {
		resp.Diagnostics.AddError("failed to generate unique ID for inventory", err.Error())
		return
	}
	data.Id = types.StringValue(newUUID.String()[:8])

	f, err := os.CreateTemp("", "imagetest-")
	if err != nil {
		resp.Diagnostics.AddError("failed to create temp file", err.Error())
		return
	}
	defer func() {
		closeErr := f.Close()
		if closeErr != nil {
			panic(fmt.Errorf("failed to close temporary file: %w", closeErr))
		}
	}()

	data.Seed = types.StringValue(f.Name())

	if err := d.store.Inventory(data).Create(ctx); err != nil {
		resp.Diagnostics.AddError("failed to create inventory", err.Error())
		return
	}

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
