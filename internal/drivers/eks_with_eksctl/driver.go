package ekswitheksctl

import (
	"bytes"
	"context"
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
// When opts.SetupTimeout is set, Setup() enforces it as a context deadline and
// passes it to eksctl --timeout for individual CloudFormation operations. If
// unset, eksctl uses its default of 25 minutes and Setup() is bounded only by
// the caller's context.
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
	return k, nil
}

// maxEksctlErrOutput bounds how much eksctl output is embedded in error
// messages. Errors become terraform diagnostics, which -json consumers receive
// as a single line; unbounded drain/CloudFormation dumps have produced >30MB
// diagnostics. The tail is kept since that is where the failure is reported.
const maxEksctlErrOutput = 256 * 1024

func (k *driver) eksctl(ctx context.Context, args ...string) error {
	args = append(args, "--color", "false") // Disable color output

	// CloudFormation log dumps and debug verbosity are for diagnosing cluster
	// bring-up; on delete they only amplify drain/retry noise.
	if len(args) > 0 && args[0] != "delete" {
		args = append(args,
			"--dumpLogs",     // Enable CloudFormation log dumping on failures
			"--verbose", "4", // Set maximum verbosity level
		)
	}

	// Add timeout flag if configured (zero = use eksctl default of 25m)
	if k.timeouts.Setup > 0 {
		args = append(args, "--timeout", k.timeouts.Setup.String())
	}

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

// deletePodIdentityAssociation deletes a pod identity association for EKS workload.
func (k *driver) deletePodIdentityAssociation(ctx context.Context) error {
	if err := k.eksctl(ctx, "delete", "addon", "--cluster="+k.clusterName, "--name=eks-pod-identity-agent"); err != nil {
		return fmt.Errorf("eksctl delete addon eks-pod-identity-agent: %w", err)
	}

	if k.podIdentityAssociations == nil {
		return fmt.Errorf("pod identity associations is nil")
	}

	for _, v := range k.podIdentityAssociations {
		if v == nil {
			continue
		}
		if err := k.eksctl(ctx, "delete", "podidentityassociation",
			"--region="+k.region,
			"--cluster="+k.clusterName,
			"--service-account-name="+v.serviceAccountName,
			"--namespace="+v.namespace); err != nil {
			return fmt.Errorf("eksctl delete podidentityassociation: %w", err)
		}
		log.Infof("Deleted pod identity associations for service account %s/%s for cluster %s",
			v.namespace, v.serviceAccountName, k.clusterName)
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

func (k *driver) Teardown(ctx context.Context) error {
	ctx, cancel := k.timeouts.TeardownContext(ctx)
	defer cancel()

	if v := os.Getenv("IMAGETEST_EKS_SKIP_TEARDOWN"); v == "true" {
		clog.FromContext(ctx).Info("Skipping EKS teardown due to IMAGETEST_EKS_SKIP_TEARDOWN=true")
		return nil
	}

	// Cluster deletion covers the nodegroups; no separate nodegroup deletion.
	// These are throwaway clusters, so the drain that precedes nodegroup
	// removal is pointless work: bypass PodDisruptionBudgets (rook et al.
	// create PDBs that block eviction for up to the 25m operation timeout),
	// drain nodes in parallel instead of the serial default, and continue past
	// drain errors (--force) so a stuck drain can no longer leak the cluster.
	if err := k.eksctl(ctx, "delete", "cluster", "--force", "--disable-nodegroup-eviction", "--parallel", "25", "--name", k.clusterName); err != nil {
		return fmt.Errorf("eksctl delete cluster: %w", err)
	}

	if k.podIdentityAssociations != nil {
		if err := k.deletePodIdentityAssociation(ctx); err != nil {
			return fmt.Errorf("deleting pod identity association: %w", err)
		}
	}

	return nil
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
