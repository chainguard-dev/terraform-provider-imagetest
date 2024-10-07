package framework

import (
	"github.com/hashicorp/terraform-plugin-framework/diag"
)

// JoinDiagnostics merges multiple sets of diag.Diagnostics.
func JoinDiagnostics(dd ...diag.Diagnostics) diag.Diagnostics {
	if len(dd) == 0 {
		return diag.Diagnostics{}
	}

	r := dd[0]
	dd = dd[1:]
	for _, d := range dd {
		r.Append(d...)
	}
	return r
}
