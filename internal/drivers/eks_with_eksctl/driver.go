package ekswitheksctl

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/docker"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers/pod"
	"github.com/charmbracelet/log"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	regionDefault    = "us-west-2"
	namespaceDefault = "imagetest"
	nodeTypeDefault  = "m5.large"
)

type driver struct {
	name       string
	k8sVersion string
	nodeAMI    string
	nodeType   string
	nodeCount  int
	storage    *StorageOptions
	awsProfile string
	tags       map[string]string
	timeouts   drivers.Timeouts

	region      string
	clusterName string
	namespace   string
	kubeconfig  string
	kcli        kubernetes.Interface
	kcfg        *rest.Config
	nodeGroup   string

	// run executes an eksctl command. Defaults to (*driver).eksctl; tests
	// substitute it to exercise Teardown without eksctl or AWS.
	run func(ctx context.Context, args ...string) error

	podIdentityAssociations []*podIdentityAssociation
	registries              map[string]*RegistryConfig
}

type Options struct {
	Region                  string
	KubernetesVersion       string
	NodeType                string
	NodeAMI                 string
	NodeCount               int
	Namespace               string
	Storage                 *StorageOptions
	PodIdentityAssociations []*PodIdentityAssociationOptions
	AWSProfile              string
	Tags                    map[string]string
	Timeouts                drivers.Timeouts
	Registries              map[string]*RegistryConfig
}

// RegistryConfig holds authentication configuration for a container registry.
type RegistryConfig struct {
	Auth *RegistryAuthConfig
}

// RegistryAuthConfig holds the credentials for authenticating to a container registry.
type RegistryAuthConfig struct {
	Username string
	Password string
	Auth     string
}

type StorageOptions struct {
	Size string
	Type string
}

type PodIdentityAssociationOptions struct {
	PermissionPolicyARN string // For now we support attaching just policies.
	ServiceAccountName  string
	Namespace           string
}

type podIdentityAssociation struct {
	permissionPolicyARN string // For now we support attaching just policies.
	serviceAccountName  string
	namespace           string
}

// NewDriver creates a new EKS driver instance that uses eksctl to provision and manage
// an Amazon EKS cluster for running tests.
//
// When opts.Timeouts.Setup is set, Setup() enforces it as a context deadline and
// passes it to eksctl --timeout for individual CloudFormation operations. If
// unset, eksctl uses its default of 25 minutes and Setup() is bounded only by
// the caller's context.
//
// Teardown() is always bounded: by opts.Timeouts.Teardown if set, otherwise by
// teardownTimeoutDefault. The bound is enforced both as a context deadline and
// as eksctl --timeout on every delete. Nodes are not drained on teardown.
func NewDriver(name string, opts Options) (drivers.Tester, error) {
	k := &driver{
		name:       name,
		k8sVersion: opts.KubernetesVersion,
		region:     opts.Region,
		nodeAMI:    opts.NodeAMI,
		nodeType:   opts.NodeType,
		nodeCount:  opts.NodeCount,
		namespace:  opts.Namespace,
		storage:    opts.Storage,
		awsProfile: opts.AWSProfile,
		tags:       opts.Tags,
		timeouts:   opts.Timeouts,
	}
	if k.region == "" {
		k.region = regionDefault
	}
	if k.namespace == "" {
		k.namespace = namespaceDefault
	}
	if k.nodeType == "" {
		k.nodeType = nodeTypeDefault
	}
	if k.nodeCount <= 0 {
		k.nodeCount = 1 // Default to 1 node if not specified
	}
	if opts.PodIdentityAssociations != nil {
		for _, v := range opts.PodIdentityAssociations {
			if v == nil {
				continue
			}
			k.podIdentityAssociations = append(k.podIdentityAssociations, &podIdentityAssociation{
				namespace:           v.Namespace,
				permissionPolicyARN: v.PermissionPolicyARN,
				serviceAccountName:  v.ServiceAccountName,
			})
		}
	}

	if opts.Registries != nil {
		k.registries = opts.Registries
	}

	if _, err := exec.LookPath("eksctl"); err != nil {
		return nil, fmt.Errorf("eksctl not found in $PATH: %w", err)
	}
	k.run = k.eksctl

	return k, nil
}

// maxEksctlErrOutput bounds how much eksctl output is embedded in error
// messages. Errors become terraform diagnostics, which -json consumers receive
// as a single line; unbounded drain/CloudFormation dumps have produced >30MB
// diagnostics. The tail is kept since that is where the failure is reported.
const maxEksctlErrOutput = 256 * 1024

// teardownTimeoutDefault bounds teardown when no teardown timeout is
// configured. It is applied both as the Teardown() context deadline and as
// eksctl --timeout on every delete, so a stuck delete can never run for the
// eksctl default of 25 minutes per operation and outlive the short-lived
// credentials CI jobs typically run with. Deleting the nodegroup stack and
// then the cluster stack takes on the order of 10 minutes.
const teardownTimeoutDefault = 20 * time.Minute

// eksctlTimeoutGrace is how much earlier than the Teardown() deadline eksctl's
// own --timeout fires on deletes, so that a stuck delete ends with eksctl's
// report of what it was waiting on rather than a bare "signal: killed".
const eksctlTimeoutGrace = 30 * time.Second

// teardownTimeout returns the configured teardown timeout, or
// teardownTimeoutDefault when unset.
func (k *driver) teardownTimeout() time.Duration {
	if k.timeouts.Teardown > 0 {
		return k.timeouts.Teardown
	}
	return teardownTimeoutDefault
}

// eksctlArgs returns the full eksctl argument list for args, with the common
// flags appended. For deletes, --timeout is the time left until ctx's
// deadline (see Teardown) so eksctl gives up, and reports why, before the
// context kills it. Without a deadline the teardown timeout is used.
func (k *driver) eksctlArgs(ctx context.Context, args ...string) []string {
	args = append(args, "--color", "false") // Disable color output

	isDelete := len(args) > 0 && args[0] == "delete"

	// CloudFormation log dumps and debug verbosity are for diagnosing cluster
	// bring-up; on delete they only amplify drain/retry noise.
	if !isDelete {
		args = append(args,
			"--dumpLogs",     // Enable CloudFormation log dumping on failures
			"--verbose", "4", // Set maximum verbosity level
		)
	}

	// --timeout bounds each long-running eksctl operation (CloudFormation
	// waits, drains). Deletes are always bounded, see teardownTimeout. For
	// everything else the configured setup timeout is used, if any (zero =
	// eksctl default of 25m).
	switch {
	case isDelete:
		timeout := k.teardownTimeout()
		if deadline, ok := ctx.Deadline(); ok {
			// Stop short of the deadline so eksctl reports its own timeout
			// (and what it was waiting on) before the context kills it.
			timeout = max((time.Until(deadline) - eksctlTimeoutGrace).Truncate(time.Second), time.Second)
		}
		args = append(args, "--timeout", timeout.String())
	case k.timeouts.Setup > 0:
		args = append(args, "--timeout", k.timeouts.Setup.String())
	}

	return args
}

func (k *driver) eksctl(ctx context.Context, args ...string) error {
	args = k.eksctlArgs(ctx, args...)

	cmd := exec.CommandContext(ctx, "eksctl", args...)
	clog.FromContext(ctx).Infof("Running command: eksctl %s", strings.Join(args, " "))
	cmd.Env = os.Environ() // Copy the environment
	cmd.Env = append(cmd.Env, "KUBECONFIG="+k.kubeconfig)
	if k.awsProfile != "" {
		cmd.Env = append(cmd.Env, "AWS_PROFILE="+k.awsProfile)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("eksctl %v: %v: %s", args, err, tail(out, maxEksctlErrOutput))
	}
	return nil
}

// tail returns at most limit trailing bytes of b, prefixed with a truncation
// note when output was dropped.
func tail(b []byte, limit int) []byte {
	if len(b) <= limit {
		return b
	}
	note := fmt.Sprintf("[... %d bytes truncated ...]\n", len(b)-limit)
	return append([]byte(note), b[len(b)-limit:]...)
}

// createCluster provisions the control plane (and its VPC) without nodegroups.
// The configuration is passed as a ClusterConfig rather than flags so that
// cluster logging is part of the initial create: enabling it afterwards via
// update-cluster-logging costs a separate, serialized cluster update and races
// the addon installs eksctl kicks off at the tail of the create
// (ResourceInUseException: cluster currently has an update in progress).
func (k *driver) createCluster(ctx context.Context) error {
	log := clog.FromContext(ctx)

	configFile, err := os.CreateTemp("", "eksctl-cluster-*.yaml")
	if err != nil {
		return fmt.Errorf("failed to create temporary config file: %w", err)
	}
	defer os.Remove(configFile.Name())

	const configTemplate = `apiVersion: eksctl.io/v1alpha5
kind: ClusterConfig
metadata:
  name: {{ .ClusterName }}
  region: {{ .Region }}
{{- if .Version }}
  version: {{ .Version | printf "%q" }}
{{- end }}
  tags:
{{- range $key, $value := .Tags }}
    {{ $key | printf "%q" }}: {{ $value | printf "%q" }}
{{- end }}
vpc:
  nat:
    # Nodes live in public subnets, so a NAT gateway is dead weight.
    gateway: Disable
cloudWatch:
  clusterLogging:
    enableTypes: ["*"]
`

	tmpl, err := template.New("cluster").Parse(configTemplate)
	if err != nil {
		return fmt.Errorf("failed to parse cluster template: %w", err)
	}

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, map[string]any{
		"ClusterName": k.clusterName,
		"Region":      k.region,
		"Version":     k.k8sVersion,
		"Tags":        k.buildTags(),
	})
	if err != nil {
		return fmt.Errorf("failed to execute cluster template: %w", err)
	}
	configContent := buf.String()

	log.Infof("Using cluster config:\n%s", configContent)

	if _, err := configFile.WriteString(configContent); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}
	if err := configFile.Close(); err != nil {
		return fmt.Errorf("failed to close config file: %w", err)
	}

	if err := k.eksctl(ctx, "create", "cluster", "--config-file="+configFile.Name(), "--kubeconfig="+k.kubeconfig); err != nil {
		return fmt.Errorf("eksctl create cluster: %w", err)
	}

	log.Infof("Created cluster %s without nodegroups, cluster logging enabled", k.clusterName)
	return nil
}

// createNodeGroup adds a self-managed nodegroup to the cluster. Self-managed
// (unmanaged) nodegroups are a plain autoscaling group in CloudFormation: EKS
// managed nodegroups add a provisioning wait on create and, on delete, an
// EKS-side drain that dominated teardown time for these throwaway clusters.
// eksctl renders the launch template (AMI, instance type, volumes) from the
// nodegroup spec, so no separate launch template is created.
func (k *driver) createNodeGroup(ctx context.Context) error {
	log := clog.FromContext(ctx)

	nodeGroupName := fmt.Sprintf("ng-%s", uuid.New().String())
	k.nodeGroup = nodeGroupName

	var storageSize int
	if k.storage != nil && k.storage.Size != "" {
		if _, err := fmt.Sscanf(k.storage.Size, "%dGB", &storageSize); err != nil {
			return fmt.Errorf("failed to parse storage size '%s': %w", k.storage.Size, err)
		}
	}

	// Create a temporary file for the eksctl config
	configFile, err := os.CreateTemp("", "eksctl-config-*.yaml")
	if err != nil {
		return fmt.Errorf("failed to create temporary config file: %w", err)
	}
	defer os.Remove(configFile.Name())

	const configTemplate = `apiVersion: eksctl.io/v1alpha5
kind: ClusterConfig
metadata:
  name: {{ .ClusterName }}
  region: {{ .Region }}
nodeGroups:
- name: {{ .NodeGroup }}
  desiredCapacity: {{ .NodeCount }}
  amiFamily: {{ .AMIFamily }}
{{- if .AMI }}
  ami: {{ .AMI }}
{{- end }}
  instanceType: {{ .InstanceType }}
  volumeSize: 80
  volumeType: gp3
{{- if .StorageSize }}
  additionalVolumes:
  - volumeName: /dev/xvdb
    volumeSize: {{ .StorageSize }}
    volumeType: {{ .StorageType }}
{{- end }}
  tags:
{{- range $key, $value := .Tags }}
    {{ $key | printf "%q" }}: {{ $value | printf "%q" }}
{{- end }}
`

	tmpl, err := template.New("nodegroup").Parse(configTemplate)
	if err != nil {
		return fmt.Errorf("failed to parse nodegroup template: %w", err)
	}

	storageType := "gp3"
	if k.storage != nil && k.storage.Type != "" {
		storageType = k.storage.Type
	}

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, map[string]any{
		"ClusterName":  k.clusterName,
		"Region":       k.region,
		"NodeGroup":    k.nodeGroup,
		"NodeCount":    k.nodeCount,
		"AMIFamily":    k.amiFamily(),
		"AMI":          k.nodeAMI,
		"InstanceType": k.nodeType,
		"StorageSize":  storageSize,
		"StorageType":  storageType,
		"Tags":         k.buildTags(),
	})
	if err != nil {
		return fmt.Errorf("failed to execute nodegroup template: %w", err)
	}
	configContent := buf.String()

	log.Infof("Using nodegroup config:\n%s", configContent)

	if _, err := configFile.WriteString(configContent); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}
	if err := configFile.Close(); err != nil {
		return fmt.Errorf("failed to close config file: %w", err)
	}

	if err := k.eksctl(ctx, "create", "nodegroup", "--config-file="+configFile.Name()); err != nil {
		return fmt.Errorf("eksctl create nodegroup: %w", err)
	}

	log.Infof("Created nodegroup %s with %d nodes for cluster %s", nodeGroupName, k.nodeCount, k.clusterName)
	return nil
}

// createPodIdentityAssociation creates a pod identity association for EKS workload.
// Please refer to the official documentation of eksctl:
//
//	https://docs.aws.amazon.com/eks/latest/eksctl/pod-identity-associations.html
func (k *driver) createPodIdentityAssociation(ctx context.Context) error {
	// The Pod Identity agent addon must be installed first.
	if err := k.eksctl(ctx, "create", "addon", "--cluster="+k.clusterName, "--name=eks-pod-identity-agent"); err != nil {
		return fmt.Errorf("eksctl create addon eks-pod-identity-agent: %w", err)
	}

	if k.podIdentityAssociations == nil {
		return fmt.Errorf("pod identity associations is nil")
	}

	for _, v := range k.podIdentityAssociations {
		if v == nil {
			continue
		}
		if err := k.eksctl(ctx, "create", "podidentityassociation",
			"--region="+k.region,
			"--cluster="+k.clusterName,
			"--service-account-name="+v.serviceAccountName,
			"--namespace="+v.namespace,
			"--permission-policy-arns="+v.permissionPolicyARN); err != nil {
			return fmt.Errorf("eksctl create podidentityassociation: %w", err)
		}
		log.Infof("Created pod identity association for service account %s/%s and policy ARN %s for cluster %s",
			v.namespace, v.serviceAccountName, v.permissionPolicyARN, k.clusterName)
	}

	return nil
}

func (k *driver) Setup(ctx context.Context) error {
	if k.timeouts.Setup > 0 {
		// Validate: the setup timeout must leave room for test execution
		// within the caller's deadline.
		if deadline, ok := ctx.Deadline(); ok {
			if remaining := time.Until(deadline); k.timeouts.Setup >= remaining {
				return fmt.Errorf("setup timeout (%s) >= remaining resource timeout (%s); the resource timeout must leave room for test execution after setup completes", k.timeouts.Setup, remaining.Truncate(time.Second))
			}
		}
	}

	ctx, cancel := k.timeouts.SetupContext(ctx)
	defer cancel()

	log := clog.FromContext(ctx)
	span := trace.SpanFromContext(ctx)

	if n, ok := os.LookupEnv("IMAGETEST_EKS_CLUSTER"); ok {
		log.Infof("Using cluster name from IMAGETEST_EKS_CLUSTER: %s", n)
		k.clusterName = n
	} else {
		uid := "imagetest-" + uuid.New().String()
		log.Infof("Using random cluster name: %s", uid)
		k.clusterName = uid
	}

	cfg, err := os.Create(filepath.Join(os.TempDir(), k.clusterName))
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	log.Infof("Using kubeconfig: %s", cfg.Name())
	k.kubeconfig = cfg.Name()

	usingExistingCluster := false
	if _, ok := os.LookupEnv("IMAGETEST_EKS_CLUSTER"); ok {
		if err := k.eksctl(ctx, "utils", "write-kubeconfig", "--cluster", k.clusterName, "--region", k.region, "--kubeconfig", k.kubeconfig); err != nil {
			return fmt.Errorf("eksctl utils write-kubeconfig: %w", err)
		}
		usingExistingCluster = true
	}

	if !usingExistingCluster {
		if err := k.createCluster(ctx); err != nil {
			return err
		}
		span.AddEvent("eks.cluster.created")
	}

	if err := k.createNodeGroup(ctx); err != nil {
		return err
	}
	span.AddEvent("eks.nodegroup.created")

	if k.podIdentityAssociations != nil {
		if err = k.createPodIdentityAssociation(ctx); err != nil {
			return fmt.Errorf("creating pod identity association: %w", err)
		}
		span.AddEvent("eks.identity.configured")
	}

	config, err := clientcmd.BuildConfigFromFlags("", k.kubeconfig)
	if err != nil {
		return fmt.Errorf("building kubeconfig: %w", err)
	}
	k.kcfg = config

	kcli, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("creating kubernetes client: %w", err)
	}
	k.kcli = kcli

	return nil
}

// teardownCommands returns the eksctl commands Teardown runs, in order. Each
// command is attempted even if an earlier one failed. Only the last one, the
// cluster delete, decides whether teardown failed: `delete cluster --wait`
// removes any nodegroup stacks still present, so if it succeeds nothing has
// leaked no matter what happened before. A failed nodegroup delete is
// expected whenever Setup failed before the nodegroup stack existed (the
// name is recorded before the create runs, and the provider tears down after
// a failed Setup too); eksctl then cannot find it and errors.
//
// The nodegroup is deleted first, on its own, with --drain=false. These are
// throwaway clusters, so draining is pointless work, and it is also the step
// that used to leak clusters: `eksctl delete cluster` drains every
// self-managed nodegroup it finds and, even with eviction disabled, waits for
// the pods to disappear. Workloads pinned by PodDisruptionBudgets or storage
// mounts (rook/ceph et al.) never do, so the drain ran into the operation
// timeout (25m by default), by which time short-lived CI credentials had
// expired and every following AWS call failed without deleting anything.
// `eksctl delete nodegroup` is the only delete that can skip the drain
// outright, so it runs first and waits for the stack to be gone. The cluster
// delete then finds no nodegroup stacks and has nothing to drain.
//
// The cluster delete keeps --force (continue past errors, e.g. an unreachable
// control plane), and keeps the PDB-bypassing, parallel drain flags as a
// fallback for the case where the nodegroup delete failed and its stack is
// still around. --wait makes eksctl report CloudFormation failures (e.g. a
// stack stuck in DELETE_FAILED on dangling ENIs) instead of exiting after
// the delete request is accepted, so leaks surface as errors.
//
// Pod identity role stacks and addons are removed by the cluster delete, so
// there is no separate deletion for them.
func (k *driver) teardownCommands() [][]string {
	var cmds [][]string
	if k.nodeGroup != "" {
		cmds = append(cmds, []string{
			"delete", "nodegroup",
			"--cluster", k.clusterName,
			"--region", k.region,
			"--name", k.nodeGroup,
			"--drain=false",
			"--wait",
		})
	}
	cmds = append(cmds, []string{
		"delete", "cluster",
		"--name", k.clusterName,
		"--region", k.region,
		"--force",
		"--disable-nodegroup-eviction",
		"--parallel", "25",
		"--wait",
	})
	return cmds
}

// Teardown deletes the nodegroup and then the cluster, see teardownCommands.
// The whole teardown is bounded by the teardown timeout (default
// teardownTimeoutDefault), detached from the caller's cancellation. Failures
// are logged at error level, including the tail of the eksctl output, since
// callers may downgrade the returned error to a warning and a failed teardown
// means AWS resources have leaked.
func (k *driver) Teardown(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), k.teardownTimeout())
	defer cancel()

	log := clog.FromContext(ctx)

	if v := os.Getenv("IMAGETEST_EKS_SKIP_TEARDOWN"); v == "true" {
		log.Info("Skipping EKS teardown due to IMAGETEST_EKS_SKIP_TEARDOWN=true")
		return nil
	}

	cmds := k.teardownCommands()
	var errs []error
	for i, args := range cmds {
		err := k.run(ctx, args...)
		if err == nil {
			continue
		}
		err = fmt.Errorf("eksctl %s %s: %w", args[0], args[1], err)
		if i < len(cmds)-1 {
			// Not final: the cluster delete below still removes whatever is
			// left. Logged at warning level in case it does not, then this
			// is the context for that failure.
			log.Warnf("%v (continuing with the cluster delete, which removes any remaining nodegroup)", err)
			errs = append(errs, err)
			continue
		}
		errs = append(errs, err)
		log.Errorf("Teardown of EKS cluster %s (region %s) failed, AWS resources have likely leaked and need manual cleanup: %s", k.clusterName, k.region, k.manualCleanup())
		return errors.Join(errs...)
	}

	log.Infof("Deleted EKS cluster %s", k.clusterName)
	return nil
}

// manualCleanup is the command sequence to remove the cluster by hand. It is
// the same order Teardown uses, since a plain `eksctl delete cluster` would
// run into the very drain that Teardown avoids.
func (k *driver) manualCleanup() string {
	var b strings.Builder
	for i, args := range k.teardownCommands() {
		if i > 0 {
			b.WriteString(" && ")
		}
		b.WriteString("eksctl ")
		b.WriteString(strings.Join(args, " "))
	}
	return b.String()
}

func (k *driver) Run(ctx context.Context, ref name.Reference) (*drivers.RunResult, error) {
	// Build docker config from registries for pod authentication
	dcfg := &docker.DockerConfig{
		Auths: make(map[string]docker.DockerAuthConfig, len(k.registries)),
	}
	for reg, cfg := range k.registries {
		if cfg.Auth == nil {
			continue
		}
		dcfg.Auths[reg] = docker.DockerAuthConfig{
			Username: cfg.Auth.Username,
			Password: cfg.Auth.Password,
			Auth:     cfg.Auth.Auth,
		}
	}

	return pod.Run(ctx, k.kcfg,
		pod.WithImageRef(ref),
		pod.WithExtraEnvs(map[string]string{
			"IMAGETEST_DRIVER": "eks_with_eksctl",
		}),
		pod.WithRegistryStaticAuth(dcfg),
	)
}

func (k *driver) amiFamily() string {
	return "AmazonLinux2023"
}

func (k *driver) buildTags() map[string]string {
	tags := map[string]string{
		"imagetest":              "true",
		"imagetest:test-name":    k.name,
		"imagetest:cluster-name": k.clusterName,
	}
	maps.Copy(tags, k.tags)
	return tags
}
