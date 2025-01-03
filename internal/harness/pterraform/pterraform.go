// Package pterraform provides a harness that runs arbitrary terraform on a
// given path. Subsequent steps are run against arbitrary infrastructure
// created by the terraform run configured via the terraform output.
package pterraform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/log"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/sandbox"
	"github.com/hashicorp/terraform-exec/tfexec"
	"k8s.io/apimachinery/pkg/util/wait"
)

var _ harness.Harness = &pterraform{}

type pterraform struct {
	// source is the filesystem with the terraform source
	source fs.FS
	vars   any

	// work is the path with the terraform workdir containing a working
	// copy of the source FS
	work string

	tf    *tfexec.Terraform
	stack *harness.Stack

	runner sandbox.Runner
}

func New(ctx context.Context, source fs.FS, opts ...Option) (*pterraform, error) {
	p := &pterraform{
		source: source,
		stack:  harness.NewStack(),
	}

	for _, opt := range opts {
		if err := opt(p); err != nil {
			return nil, err
		}
	}

	if p.work == "" {
		path, err := os.MkdirTemp("", "pterraform")
		if err != nil {
			return nil, err
		}
		p.work = path
	} else {
		// Ensure the working directory exists
		if err := os.MkdirAll(p.work, 0755); err != nil {
			return nil, err
		}
	}

	tf, err := tfexec.NewTerraform(p.work, "terraform")
	if err != nil {
		return nil, fmt.Errorf("failed to find a terraform executable on $PATH: %w", err)
	}
	p.tf = tf
	p.tf.SetStdout(io.Discard)

	// Use the host variables but ignore any host TF_VAR_
	envs := make(map[string]string)
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "TF_VAR_") {
			continue
		}
		parts := strings.SplitN(e, "=", 2)
		if len(parts) != 2 {
			continue
		}
		envs[parts[0]] = parts[1]
	}

	// The TF_VAR_ above isn't enough, for example we are failing on:
	//
	// > setting environment variables: manual setting of env var "TF_LOG_PROVIDER" detected
	//
	// We'll keep the TF_VAR_ filtering just to make the warning less noisy, but also drop
	// and warn about any other prohibited environment variables.
	for _, prohibited := range tfexec.ProhibitedEnv(envs) {
		log.Warn(ctx, "removing prohibited env", "env", prohibited)
		delete(envs, prohibited)
	}

	if err := p.tf.SetEnv(envs); err != nil {
		return nil, fmt.Errorf("setting environment variables: %w", err)
	}

	return p, nil
}

// Create implements harness.Harness.
func (p *pterraform) Create(ctx context.Context) error {
	// create a list of known skips for terraform related files
	skips := []func(fs.DirEntry) bool{
		// skip the .terraform directory
		func(de fs.DirEntry) bool {
			return strings.HasPrefix(de.Name(), ".terraform")
		},
		// skip the terraform.tfstate file and any files in the .terraform.lock.hcl directory
		func(de fs.DirEntry) bool {
			return strings.HasPrefix(de.Name(), "terraform.tfstate") || strings.HasPrefix(de.Name(), "terraform.tfstate.backup") || strings.HasPrefix(de.Name(), ".terraform.lock.hcl")
		},
	}

	// Clean the working directory of any files that may have been left over from a previous run
	if err := filepath.WalkDir(p.work, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// skip known skips
		for _, skip := range skips {
			if skip(d) {
				return nil
			}
		}

		// skip symlinks
		if d.Type() == fs.ModeSymlink {
			return nil
		}

		if d.IsDir() {
			return nil
		}

		// Only remove the remaining files that may be terraform files
		tfs := []func(fs.DirEntry) bool{
			// identifies any terraform source files
			func(de fs.DirEntry) bool {
				return strings.HasSuffix(de.Name(), ".tf") || strings.HasSuffix(de.Name(), ".tfvars.json") || strings.HasSuffix(de.Name(), ".tfvars")
			},
		}
		for _, tf := range tfs {
			if !tf(d) {
				return nil
			}
		}

		if err := os.Remove(path); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return err
	}

	// Copy the source directory to the working directory, skipping symlinks
	if err := fs.WalkDir(p.source, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// skip known skips
		for _, skip := range skips {
			if skip(d) {
				return nil
			}
		}

		// skip symlinks
		if d.Type() == fs.ModeSymlink {
			return nil
		}

		targ := filepath.Join(p.work, filepath.FromSlash(path))
		if d.IsDir() {
			if err := os.MkdirAll(targ, 0755); err != nil {
				return err
			}
			return nil
		}

		r, err := p.source.Open(path)
		if err != nil {
			return err
		}
		defer r.Close()

		// skip errors for non-existent files
		info, err := r.Stat()
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("failed to stat file: %w", err)
		}

		w, err := os.OpenFile(targ, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
		if err != nil {
			return err
		}
		defer w.Close()

		if _, err := io.Copy(w, r); err != nil {
			return err
		}

		return nil
	}); err != nil {
		return err
	}

	initopts := []tfexec.InitOption{
		tfexec.Upgrade(true),
		tfexec.Reconfigure(true),
	}

	if err := p.stack.Add(func(ctx context.Context) error {
		return os.RemoveAll(p.work)
	}); err != nil {
		return fmt.Errorf("adding terraform destroy to stack: %w", err)
	}

	if err := p.tf.Init(ctx, initopts...); err != nil {
		return fmt.Errorf("failed to initialize terraform: %w", err)
	}

	applyopts := []tfexec.ApplyOption{}

	for _, opt := range p.evars() {
		applyopts = append(applyopts, opt)
	}

	if p.vars != nil {
		// Write the vars as a vars.tf.json file
		vdata, err := json.Marshal(p.vars)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(p.work, "vars.tfvars.json"), vdata, 0644); err != nil {
			return err
		}
		applyopts = append(applyopts, tfexec.VarFile("vars.tfvars.json"))
	}

	if err := p.tf.Apply(ctx, applyopts...); err != nil {
		return fmt.Errorf("failed to apply terraform: %w", err)
	}

	if err := p.stack.Add(func(ctx context.Context) error {
		destroyopts := []tfexec.DestroyOption{}
		for _, opt := range p.evars() {
			destroyopts = append(destroyopts, opt)
		}
		return p.tf.Destroy(ctx, destroyopts...)
	}); err != nil {
		return fmt.Errorf("adding terraform destroy to stack: %w", err)
	}

	out, err := p.tf.Output(ctx)
	if err != nil {
		return fmt.Errorf("failed to get terraform output: %w", err)
	}

	connectionRaw, ok := out["connection"]
	if !ok {
		return fmt.Errorf("no connection output")
	}

	var conn *Connection
	if err := json.Unmarshal(connectionRaw.Value, &conn); err != nil {
		return fmt.Errorf("decoding connection details: %w", err)
	}

	if conn.Retry != nil {
		if conn.Retry.Delay == "" {
			conn.Retry.Delay = "0s"
		}
		retry, err := time.ParseDuration(conn.Retry.Delay)
		if err != nil {
			return fmt.Errorf("failed to parse retry delay: %w", err)
		}
		conn.backoff = wait.Backoff{
			Steps:    conn.Retry.Attempts,
			Duration: retry,
			Factor:   conn.Retry.Factor,
		}
	} else {
		// The equivalent to a single try
		conn.backoff = wait.Backoff{
			Steps:    1,
			Duration: 0,
			Factor:   1.0,
		}
	}

	if conn.Docker != nil {
		conn.Docker.PrivateKeyPath = filepath.Join(p.work, conn.Docker.PrivateKeyPath)

		if err := wait.ExponentialBackoffWithContext(ctx, conn.backoff, func(ctx context.Context) (bool, error) {
			c, err := newDockerRunner(ctx, conn.Docker)
			if err != nil {
				log.Warn(ctx, "failed to create docker runner", "error", err)
				return false, nil
			}
			p.runner = c
			return true, nil
		}); err != nil {
			return fmt.Errorf("waiting for docker connection to be ready: %w", err)
		}

	} else if conn.Kubernetes != nil {
		if conn.Kubernetes.KubeconfigPath != "" {
			conn.Kubernetes.KubeconfigPath = filepath.Join(p.work, conn.Kubernetes.KubeconfigPath)
		}

		sbx, err := conn.Kubernetes.runner()
		if err != nil {
			return err
		}

		if err := wait.ExponentialBackoffWithContext(ctx, conn.backoff, func(ctx context.Context) (bool, error) {
			r, err := sbx.Start(ctx)
			if err != nil {
				return false, err
			}
			p.runner = r

			return true, nil
		}); err != nil {
			return fmt.Errorf("waiting for kubernetes connection to be ready: %w", err)
		}

	} else {
		return fmt.Errorf("unknown connection type")
	}

	return nil
}

// Run implements harness.Harness.
func (p *pterraform) Run(ctx context.Context, cmd harness.Command) error {
	return p.runner.Run(ctx, cmd)
}

func (p *pterraform) Destroy(ctx context.Context) error {
	return p.stack.Teardown(ctx)
}

// evars slurps any IMAGETEST_TF_VAR_* environment variables and adds them to
// the pterraform executor as -var="key=value".
func (p *pterraform) evars() []*tfexec.VarOption {
	opts := make([]*tfexec.VarOption, 0)

	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "TF_VAR_") {
			continue
		}

		parts := strings.SplitN(e, "=", 2)
		if len(parts) != 2 {
			continue
		}

		k, v := parts[0], parts[1]

		if strings.HasPrefix(k, "IMAGETEST_TF_VAR_") {
			k = strings.TrimPrefix(k, "IMAGETEST_TF_VAR_")
			opts = append(opts, tfexec.Var(fmt.Sprintf("%s=%s", k, v)))
		}
	}

	return opts
}

type Option func(*pterraform) error

// WithWorkspace sets the path to the terraform workspace to use.
func WithWorkspace(workspace string) Option {
	return func(p *pterraform) error {
		p.work = workspace
		return nil
	}
}

func WithVars(vars json.RawMessage) Option {
	return func(p *pterraform) error {
		p.vars = vars
		return nil
	}
}
