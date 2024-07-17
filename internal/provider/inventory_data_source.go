package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"k8s.io/apimachinery/pkg/util/rand"
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

	// NOTE: We don't need internet scale collision resistance here, just on the
	// order of ~images. Pick 6 to give us resistance (~<1%) across ~10^4 entries.
	data.Seed = types.StringValue(rand.String(6))

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
