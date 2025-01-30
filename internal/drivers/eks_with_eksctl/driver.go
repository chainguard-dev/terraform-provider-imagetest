package ekswitheksctl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/uuid"
)

type driver struct {
	Namespace string
}

func NewDriver(n string, opts ...DriverOpts) (drivers.Tester, error) {
	k := &driver{
		Namespace: "imagetest",
	}

	if _, err := exec.LookPath("eksctl"); err != nil {
		return nil, fmt.Errorf("eksctl not found in $PATH: %w", err)
	}

	for _, opt := range opts {
		if err := opt(k); err != nil {
			return nil, err
		}
	}

	return k, nil
}

func (k *driver) eksctl(ctx context.Context, args ...string) error {
	args = append(args, []string{
		"--color", "false", // Disable color output
		"--region", "us-west-2", // TODO: make region configurable
	}...)
	clog.FromContext(ctx).Infof("eksctl %v", args)
	cmd := exec.CommandContext(ctx, "eksctl", args...)
	cmd.Env = os.Environ() // TODO: add more?
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("eksctl %v: %v: %s", args, err, out)
	}
	return nil
}

func clusterName(ctx context.Context) string {
	if n, ok := os.LookupEnv("IMAGETEST_EKS_CLUSTER"); ok {
		clog.FromContext(ctx).Infof("Using cluster name from IMAGETEST_EKS_CLUSTER: %s", n)
		return n
	}
	uid := "imagetest-" + uuid.New().String()
	clog.FromContext(ctx).Infof("Using random cluster name: %s", uid)
	return uid
}

func (k *driver) Setup(ctx context.Context) error {
	if err := k.eksctl(ctx, "create", "cluster", "--name", clusterName(ctx)); err != nil {
		return fmt.Errorf("eksctl create cluster: %w", err)
	}
	return k.preflight(ctx)
}

func (k *driver) Teardown(ctx context.Context) error {
	if v := os.Getenv("IMAGETEST_EKS_SKIP_TEARDOWN"); v == "true" {
		clog.FromContext(ctx).Info("Skipping EKS teardown")
		return nil
	}
	if err := k.eksctl(ctx, "delete", "cluster", "--name", clusterName(ctx)); err != nil {
		return fmt.Errorf("eksctl delete cluster: %w", err)
	}
	return nil
}

func (k *driver) Run(ctx context.Context, ref name.Reference) error {
	// TODO: Implement this, create pod or whatever.
	return errors.New("not yet implemented")
}

// preflight creates the necessary k8s resources to run the tests in pods.
func (k *driver) preflight(context.Context) error {
	// TODO: Implement this, create namespace or whatever.
	return errors.New("not yet implemented")
}
