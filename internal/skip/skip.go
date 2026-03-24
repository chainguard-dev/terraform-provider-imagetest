// Package skip contains logic for determining if a test should be skipped.
package skip

import "strings"

// Skip evaluates a test's labels against a set of labels that should be
// included in the test run and a set of labels that should be excluded from
// the test run. If both inclusion and exclusion labels are provided, exclusion
// is evaluated last. This allows defining buckets of tests to run which
// exclude undesirable subsets, e.g.:
//
//		Include: size=small
//	 Exclude: flaky=true
//
// Will execute all of the 'small' tests while skipping any tests that are
// marked flaky.
func Skip(t, include, exclude map[string]string) (bool, string) {
	switch {
	// Can't skip without inclusion or exclusion rules.
	case len(include) == 0 && len(exclude) == 0:
		return false, ""
	case len(include) != 0:
		var reason strings.Builder
		for k, v := range include {
			if t[k] != v {
				reason.WriteString(k + "=" + v + " ")
			}
		}
		if reason.Len() == 0 {
			return shouldExclude(t, exclude)
		}
		return true, "skipped due to missing required labels: " + strings.TrimSpace(reason.String())
	case len(exclude) != 0:
		return shouldExclude(t, exclude)
	default:
		return false, ""
	}
}

func shouldExclude(a, b map[string]string) (bool, string) {
	var reason strings.Builder
	for k, v := range b {
		if a[k] == v {
			reason.WriteString(k + "=" + v + " ")
		}
	}
	if reason.Len() == 0 {
		return false, ""
	}
	return true, "skipped due to presence of excluded labels: " + strings.TrimSpace(reason.String())
}
