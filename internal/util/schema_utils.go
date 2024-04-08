package util

import "github.com/hashicorp/terraform-plugin-framework/resource/schema"

func MergeSchemaMaps(maps ...map[string]schema.Attribute) map[string]schema.Attribute {
	result := make(map[string]schema.Attribute)

	for _, m := range maps {
		for k, v := range m {
			result[k] = v
		}
	}

	return result
}
