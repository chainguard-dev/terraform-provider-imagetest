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

// MergeLabelMaps will create a new map containing all items from maps passed as parameters. If multiple maps define the
// same key it will be overwritten by the last occurrence of key in the list of maps received.
func MergeLabelMaps(maps ...map[string]string) map[string]string {
	result := make(map[string]string)

	for _, m := range maps {
		for k, v := range m {
			result[k] = v
		}
	}

	return result
}
