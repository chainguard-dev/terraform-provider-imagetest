package sshhost

import (
	"context"
	"errors"
	"fmt"

	"github.com/chainguard-dev/clog"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// Labels on the containers the driver creates. LabelRun is the per-run
// label: its value is the run's name, the same key the ec2 driver tags
// its AWS resources with. The driver exports the key and the value as
// environment variables (see RunEnv) so a test can put the same label on
// the containers it starts through the Docker socket. Teardown then
// removes them with the run. LabelLock names the lock path a run held, so
// the stale sweep touches only runs that took the same lock.
const (
	LabelRun  = "imagetest:test-name"
	LabelLock = "imagetest:lock"

	EnvRunLabel = "IMAGETEST_RUN_LABEL"
	EnvRun      = "IMAGETEST_RUN"
)

// RunEnv returns the environment variables that name the run's label, for
// the test container: IMAGETEST_RUN_LABEL is the label key and
// IMAGETEST_RUN its value, so a script can pass
// --label "$IMAGETEST_RUN_LABEL=$IMAGETEST_RUN" to docker run.
func (h *Host) RunEnv() map[string]string {
	return map[string]string{
		EnvRunLabel: LabelRun,
		EnvRun:      h.Name,
	}
}

// isRunContainer reports whether labels mark a container as belonging to
// run. An empty run matches nothing: a missing label must never read as a
// match, or a sweep would take every unlabelled container on the host.
func isRunContainer(labels map[string]string, run string) bool {
	r, ok := labels[LabelRun]
	return ok && run != "" && r == run
}

// staleRuns returns the names of the other runs that held lockPath, read
// from the driver containers they left behind. A run that took no lock,
// or another lock, is not stale: it may be live, and its containers are
// not this lock holder's to remove. A run from before lock labels, or a
// sibling with only a run label, never adds a run by itself.
func staleRuns(all []container.Summary, lockPath, run string) map[string]bool {
	stale := map[string]bool{}
	if lockPath == "" {
		return stale
	}
	for _, c := range all {
		if c.Labels[LabelLock] != lockPath {
			continue
		}
		if r, ok := c.Labels[LabelRun]; ok && r != "" && r != run {
			stale[r] = true
		}
	}
	return stale
}

// selectStaleContainers returns every container of a run in staleRuns,
// the driver's own and the siblings the test labelled alike.
func selectStaleContainers(all []container.Summary, lockPath, run string) []container.Summary {
	stale := staleRuns(all, lockPath, run)
	return selectContainers(all, func(labels map[string]string) bool {
		r, ok := labels[LabelRun]
		return ok && stale[r]
	})
}

// RemoveRunContainers removes every container on the host that carries
// this run's label. It runs at teardown after the driver's own containers
// are removed, so a sibling the test started through the Docker socket
// dies with the run even when the test's exit trap never fired.
func (h *Host) RemoveRunContainers(ctx context.Context) error {
	if h.Name == "" {
		return errors.New("run sweep: the host has no run name")
	}
	return h.removeContainers(ctx, "run", func(all []container.Summary) []container.Summary {
		return selectContainers(all, func(labels map[string]string) bool {
			return isRunContainer(labels, h.Name)
		})
	})
}

// RemoveStaleContainers removes the containers of every other run that
// held this host's lock, see staleRuns. It is for the lock holder only: a
// guest that died mid-run never reached teardown, and its containers may
// still hold the GPU. Once this run holds the lock, no other run on the
// same lock can have live containers, so what they left is stale. Runs on
// no lock or on another lock are left alone, since they may be live.
func (h *Host) RemoveStaleContainers(ctx context.Context) error {
	if h.LockPath == "" {
		return errors.New("stale sweep: the host has no lock path")
	}
	return h.removeContainers(ctx, "stale", func(all []container.Summary) []container.Summary {
		return selectStaleContainers(all, h.LockPath, h.Name)
	})
}

func (h *Host) removeContainers(ctx context.Context, what string, pick func([]container.Summary) []container.Summary) error {
	log := clog.FromContext(ctx)

	cli, err := h.dockerClient(ctx)
	if err != nil {
		return fmt.Errorf("creating docker client: %w", err)
	}
	defer cli.Close()

	if err := h.waitDocker(ctx, cli); err != nil {
		return err
	}

	list, err := cli.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return fmt.Errorf("listing containers: %w", err)
	}

	var errs []error
	removed := 0
	for _, c := range pick(list.Items) {
		log.Info("removing "+what+" container", "id", c.ID, "names", c.Names, "labels", c.Labels)
		if _, err := cli.ContainerRemove(ctx, c.ID, client.ContainerRemoveOptions{Force: true}); err != nil && !cerrdefs.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("removing %s container %s: %w", what, c.ID, err))
			continue
		}
		removed++
	}
	log.Info("swept "+what+" containers", "removed", removed, "failed", len(errs))
	return errors.Join(errs...)
}

// selectContainers returns the containers whose labels match.
func selectContainers(all []container.Summary, match func(map[string]string) bool) []container.Summary {
	var out []container.Summary
	for _, c := range all {
		if match(c.Labels) {
			out = append(out, c)
		}
	}
	return out
}
