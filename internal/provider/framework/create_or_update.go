package framework

import (
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
)

type CreateOrUpdateRequest struct {
	Config       tfsdk.Config
	Plan         tfsdk.Plan
	State        tfsdk.State
	ProviderMeta tfsdk.Config
}
