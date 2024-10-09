package provider

import (
	"context"
	"os"

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

	f, err := os.MkdirTemp("", "imagetest-")
	if err != nil {
		resp.Diagnostics.AddError("failed to create temp file", err.Error())
		return
	}

	data.Seed = types.StringValue(f)

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
