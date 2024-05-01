package provider

import (
	"context"
	"fmt"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/log"
	petname "github.com/dustinkirkland/golang-petname"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure provider defined types fully satisfy framework interfaces.
var (
	_ datasource.DataSource = &RandomDataSource{}
)

func NewRandomDataSource() datasource.DataSource {
	return &RandomDataSource{}
}

// RandomDataSource defines the data source implementation.
type RandomDataSource struct {
	store *ProviderStore
}

// RandomDataSourceModel describes the data source data model.
type RandomDataSourceModel struct {
	Id types.String `tfsdk:"id"`
}

func (d *RandomDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_random"
}

func (d *RandomDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		// This description is used by the documentation generator and the language server.

		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true,
			},
		},
	}
}

func (d *RandomDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	ctx = log.WithCtx(ctx, d.store.Logger())
	// Prevent panic if the provider has not been configured.
	if req.ProviderData == nil {
		return
	}

	store, ok := req.ProviderData.(*ProviderStore)
	if !ok {
		log.Error(ctx, "failed to parse provider data in RandomDataSource")
		resp.Diagnostics.AddError("invalid provider data", "...")
		return
	}

	d.store = store
}

func (d *RandomDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	ctx = log.WithCtx(ctx, d.store.Logger())

	log.Debug(ctx, "Random.Read()")

	var data RandomDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		log.Error(ctx, "failed to get configuration for RandomDataSource resource")
		return
	}

	id := petname.Generate(2, "-")
	log.Debug(ctx, fmt.Sprintf("Random.Read() | %s", id))
	data.Id = types.StringValue(id)

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		log.Error(ctx, "failed to save state in RandomDataSource")
		return
	}
}
