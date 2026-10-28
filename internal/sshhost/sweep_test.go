package sshhost

import (
	"slices"
	"testing"

	"github.com/moby/moby/api/types/container"
)

func TestRunEnv(t *testing.T) {
	h := &Host{Name: "imagetest-ssh-1a2b3c4d", LockPath: "/var/lock/imagetest/gpu"}
	got := h.RunEnv()
	if got[EnvRunLabel] != "imagetest:test-name" || got[EnvRun] != "imagetest-ssh-1a2b3c4d" {
		t.Fatalf("RunEnv() = %v", got)
	}
	// The label the sweep filters on is the one the env names, and the
	// lock label is set only when a lock is.
	labels := h.Labels()
	if labels[got[EnvRunLabel]] != got[EnvRun] {
		t.Fatalf("Labels() = %v does not carry %s=%s", labels, got[EnvRunLabel], got[EnvRun])
	}
	if labels[LabelLock] != h.LockPath {
		t.Errorf("Labels()[%s] = %q, want %q", LabelLock, labels[LabelLock], h.LockPath)
	}
	if _, ok := (&Host{Name: "x"}).Labels()[LabelLock]; ok {
		t.Error("Labels() carries a lock label without a lock path")
	}
}

func ids(cs []container.Summary) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.ID)
	}
	return out
}

// TestSelectRunContainers checks that the run sweep takes only this run's
// containers, the driver's own and the labelled siblings.
func TestSelectRunContainers(t *testing.T) {
	const run = "imagetest-ssh-aaaaaaaa"
	all := []container.Summary{
		{ID: "mine-driver", Labels: map[string]string{"imagetest": "true", LabelRun: run}},
		{ID: "mine-sibling", Labels: map[string]string{LabelRun: run}},
		{ID: "other-driver", Labels: map[string]string{"imagetest": "true", LabelRun: "imagetest-ssh-bbbbbbbb"}},
		{ID: "operator", Labels: map[string]string{"app": "prune"}},
		{ID: "unlabelled"},
	}

	got := ids(selectContainers(all, func(l map[string]string) bool { return isRunContainer(l, run) }))
	if want := []string{"mine-driver", "mine-sibling"}; !slices.Equal(got, want) {
		t.Errorf("run sweep = %v, want %v", got, want)
	}

	// A run name that matches nothing takes nothing.
	if got := selectContainers(all, func(l map[string]string) bool { return isRunContainer(l, "") }); len(got) != 0 {
		t.Errorf("run sweep with empty run = %v, want none", ids(got))
	}
}

// TestSelectStaleContainers checks that the stale sweep takes every
// container of another run that held this lock, siblings included, and
// leaves runs that took no lock or another lock, this run's own, and
// anything that is not imagetest's.
func TestSelectStaleContainers(t *testing.T) {
	const (
		lock = "/var/lock/imagetest/gpu"
		run  = "imagetest-ssh-aaaaaaaa"
	)
	all := []container.Summary{
		{ID: "mine-driver", Labels: map[string]string{LabelRun: run, LabelLock: lock}},
		{ID: "mine-sibling", Labels: map[string]string{LabelRun: run}},
		// A run that held this lock and died: driver container and sibling.
		{ID: "dead-driver", Labels: map[string]string{LabelRun: "imagetest-ssh-dddddddd", LabelLock: lock}},
		{ID: "dead-sibling", Labels: map[string]string{LabelRun: "imagetest-ssh-dddddddd"}},
		// A run on no lock: an older provider, or a resource without
		// lock_path. It may be live and survives.
		{ID: "nolock-driver", Labels: map[string]string{"imagetest": "true", LabelRun: "imagetest-ssh-nnnnnnnn"}},
		{ID: "nolock-sibling", Labels: map[string]string{LabelRun: "imagetest-ssh-nnnnnnnn"}},
		// A run on another lock survives too.
		{ID: "otherlock-driver", Labels: map[string]string{LabelRun: "imagetest-ec2-oooooooo", LabelLock: "/var/lock/imagetest/cpu"}},
		// A sibling whose test wrongly set the lock label but no run
		// label adds no run.
		{ID: "lock-only", Labels: map[string]string{LabelLock: lock}},
		{ID: "operator", Labels: map[string]string{"app": "prune"}},
		{ID: "unlabelled"},
	}

	got := ids(selectStaleContainers(all, lock, run))
	if want := []string{"dead-driver", "dead-sibling"}; !slices.Equal(got, want) {
		t.Errorf("stale sweep = %v, want %v", got, want)
	}

	// Without a lock path nothing is stale.
	if got := selectStaleContainers(all, "", run); len(got) != 0 {
		t.Errorf("stale sweep without a lock = %v, want none", ids(got))
	}
}

// TestRemoveStaleContainersNeedsLock checks that the stale sweep refuses
// to run for a host without a lock path, before any connection.
func TestRemoveStaleContainersNeedsLock(t *testing.T) {
	h := &Host{Name: "imagetest-ssh-test", Addr: "127.0.0.1", Port: closedPort(t), User: "nobody"}
	if err := h.RemoveStaleContainers(t.Context()); err == nil {
		t.Fatal("RemoveStaleContainers() = nil, want error")
	}
	if err := (&Host{Addr: "127.0.0.1", Port: closedPort(t), User: "nobody"}).RemoveRunContainers(t.Context()); err == nil {
		t.Fatal("RemoveRunContainers() without a run name = nil, want error")
	}
}
