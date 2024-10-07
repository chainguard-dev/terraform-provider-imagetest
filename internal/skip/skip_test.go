package skip

import (
	"strings"
	"testing"
)

func TestSkip(t *testing.T) {
	tcs := map[string]struct {
		test   map[string]string
		inc    map[string]string
		exc    map[string]string
		exp    bool
		reason string
	}{
		"no filtering": {
			test: map[string]string{"a": "b"},
			exp:  false,
		},
		"included": {
			test: map[string]string{"a": "b", "c": "d"},
			inc:  map[string]string{"a": "b"},
			exp:  false,
		},
		"partial match inclusion": {
			test:   map[string]string{"a": "b", "size": "small"},
			inc:    map[string]string{"a": "b", "size": "medium"},
			exp:    true,
			reason: "size=medium",
		},
		"non-matching inclusion": {
			test:   map[string]string{"a": "b", "size": "small"},
			inc:    map[string]string{"c": "d"},
			exp:    true,
			reason: "c=d",
		},
		"non-matching inclusion differing value": {
			test:   map[string]string{"a": "b"},
			inc:    map[string]string{"a": "d"},
			exp:    true,
			reason: "a=d",
		},
		"non-matching inclusion exclusion present": {
			test:   map[string]string{"a": "b"},
			inc:    map[string]string{"a": "d"},
			exc:    map[string]string{"a": "c"},
			exp:    true,
			reason: "a=d",
		},
		"excluded": {
			test:   map[string]string{"size": "small", "flaky": "true"},
			exc:    map[string]string{"size": "small"},
			exp:    true,
			reason: "size=small",
		},
		"excluded when include matches": {
			test:   map[string]string{"size": "small", "flaky": "true"},
			inc:    map[string]string{"size": "small"},
			exc:    map[string]string{"flaky": "true"},
			exp:    true,
			reason: "flaky=true",
		},
		"non-matching exclusion": {
			test: map[string]string{"size": "small", "flaky": "false"},
			exc:  map[string]string{"flaky": "true"},
			exp:  false,
		},
		"non-matching exclusion, inclusion present": {
			test: map[string]string{"size": "small", "flaky": "true"},
			inc:  map[string]string{"size": "small"},
			exc:  map[string]string{"flaky": "false"},
			exp:  false,
		},
	}

	for name, tc := range tcs {
		t.Run(name, func(t *testing.T) {
			if skip, reason := Skip(tc.test, tc.inc, tc.exc); skip != tc.exp {
				t.Errorf("expected: %t, got: %t", tc.exp, skip)
				if tc.reason != "" && strings.Contains(reason, tc.reason) {
					t.Errorf("expected reason for skipping to contain '%s', got: %s",
						tc.reason, reason)
				}
			}
		})
	}
}
