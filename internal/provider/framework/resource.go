package framework

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// WithTypeName can be embedded into [resource.Resource] implementations to
// automatically wire up the resource's name as the resource name appended to
// the provider name.
type WithTypeName string

func (w WithTypeName) Metadata(
	_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse,
) {
	resp.TypeName = req.ProviderTypeName + "_" + string(w)
}

// WithNoOpDelete can be embedded into [resource.Resource] implementations that
// don't require a Delete method.
type WithNoOpDelete struct{}

func (w WithNoOpDelete) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}

// WithNoOpRead can be embedded into [resource.Resource] implementations that
// don't require a Read method.
type WithNoOpRead struct{}

func (w WithNoOpRead) Read(_ context.Context, _ resource.ReadRequest, _ *resource.ReadResponse) {
}
