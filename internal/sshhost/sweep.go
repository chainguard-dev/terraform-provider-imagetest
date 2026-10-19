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
// label: its value is the run's name, and the driver exports both as
// environment variables (see RunEnv) so a test can put the same label on
// the containers it starts through the Docker socket. Teardown then
// removes them with the run.
const (
	LabelImagetest = "imagetest"
	LabelRun       = "imagetest:run"

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

// isStaleContainer reports whether labels mark a container as imagetest's
// but not this run's: one of another run, by its run label, or one from a
// driver that set only the shared marker.
func isStaleContainer(labels map[string]string, run string) bool {
	if r, ok := labels[LabelRun]; ok {
		return r != run
	}
	return labels[LabelImagetest] == "true"
}

// RemoveRunContainers removes every container on the host that carries
// this run's label. It runs at teardown after the driver's own containers
// are removed, so a sibling the test started through the Docker socket
// dies with the run even when the test's exit trap never fired.
func (h *Host) RemoveRunContainers(ctx context.Context) error {
	if h.Name == "" {
		return errors.New("run sweep: the host has no run name")
	}
	return h.removeContainers(ctx, "run", func(labels map[string]string) bool {
		return isRunContainer(labels, h.Name)
	})
}

// RemoveStaleContainers removes every imagetest container on the host that
// is not this run's. It is for the lock holder only: a guest that died
// mid-run never reached teardown, and its containers may still hold the
// GPU. Once this run holds the host lock, no other run can have live
// containers, so anything else labelled imagetest's is stale.
func (h *Host) RemoveStaleContainers(ctx context.Context) error {
	return h.removeContainers(ctx, "stale", func(labels map[string]string) bool {
		return isStaleContainer(labels, h.Name)
	})
}

func (h *Host) removeContainers(ctx context.Context, what string, match func(map[string]string) bool) error {
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
	for _, c := range selectContainers(list.Items, match) {
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
