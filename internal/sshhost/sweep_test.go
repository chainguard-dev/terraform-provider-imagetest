package sshhost

import (
	"slices"
	"testing"

	"github.com/moby/moby/api/types/container"
)

func TestRunEnv(t *testing.T) {
	h := &Host{Name: "imagetest-ssh-1a2b3c4d"}
	got := h.RunEnv()
	if got[EnvRunLabel] != LabelRun || got[EnvRun] != "imagetest-ssh-1a2b3c4d" {
		t.Fatalf("RunEnv() = %v", got)
	}
	// The label the sweep filters on is the one the env names.
	if h.Labels()[got[EnvRunLabel]] != got[EnvRun] {
		t.Fatalf("Labels() = %v does not carry %s=%s", h.Labels(), got[EnvRunLabel], got[EnvRun])
	}
}

// TestSelectContainers checks which containers each sweep takes: the run
// sweep only this run's, the stale sweep every other imagetest container
// and nothing that is not imagetest's.
func TestSelectContainers(t *testing.T) {
	const run = "imagetest-ssh-aaaaaaaa"
	all := []container.Summary{
		{ID: "mine-driver", Labels: map[string]string{LabelImagetest: "true", LabelRun: run}},
		{ID: "mine-sibling", Labels: map[string]string{LabelRun: run}},
		{ID: "other-driver", Labels: map[string]string{LabelImagetest: "true", LabelRun: "imagetest-ssh-bbbbbbbb"}},
		{ID: "other-sibling", Labels: map[string]string{LabelRun: "imagetest-ssh-bbbbbbbb"}},
		{ID: "old-provider", Labels: map[string]string{LabelImagetest: "true", "imagetest:test-name": "imagetest-ssh-cccccccc"}},
		{ID: "operator", Labels: map[string]string{"app": "prune"}},
		{ID: "unlabelled"},
	}

	ids := func(cs []container.Summary) []string {
		var out []string
		for _, c := range cs {
			out = append(out, c.ID)
		}
		return out
	}

	got := ids(selectContainers(all, func(l map[string]string) bool { return isRunContainer(l, run) }))
	if want := []string{"mine-driver", "mine-sibling"}; !slices.Equal(got, want) {
		t.Errorf("run sweep = %v, want %v", got, want)
	}

	got = ids(selectContainers(all, func(l map[string]string) bool { return isStaleContainer(l, run) }))
	if want := []string{"other-driver", "other-sibling", "old-provider"}; !slices.Equal(got, want) {
		t.Errorf("stale sweep = %v, want %v", got, want)
	}

	// A run name that matches nothing takes nothing.
	if got := selectContainers(all, func(l map[string]string) bool { return isRunContainer(l, "") }); len(got) != 0 {
		t.Errorf("run sweep with empty run = %v, want none", ids(got))
	}
}
