package gke

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	container "cloud.google.com/go/container/apiv1"
	"cloud.google.com/go/container/apiv1/containerpb"
	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/docker"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers/pod"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

const (
	regionDefault      = "us-central1"
	nodeCountDefault   = 1
	machineTypeDefault = "e2-standard-4"
	diskSizeGBDefault  = 100
	diskTypeDefault    = "pd-standard"
	timeoutDefault     = 30 * time.Minute
	pollFrequency      = 30 * time.Second
)

type driver struct {
	name  string
	stack *harness.Stack

	// Configuration
	projectID         string
	region            string // Regional cluster (preferred)
	zone              string // Zonal cluster (alternative)
	clusterName       string
	nodeCount         int32
	machineType       string
	diskSizeGB        int32
	diskType          string
	kubernetesVersion string
	timeout           time.Duration
	tags              map[string]string

	// Runtime state
	kubeconfig string
	kcli       kubernetes.Interface
	kcfg       *rest.Config

	// GCP SDK clients
	clusterClient *container.ClusterManagerClient

	// Features
	registries map[string]*RegistryConfig
}

type Options struct {
	// GCP project ID. If empty, falls back to the GOOGLE_CLOUD_PROJECT
	// environment variable, then the deprecated GOOGLE_PROJECT_ID env var.
	// Required after the fallback chain.
	Project string
	// Regional cluster location (e.g., "us-central1"). Mutually exclusive with Zone.
	Region string
	// Zonal cluster location (e.g., "us-central1-a"). Mutually exclusive with Region.
	Zone string
	// The GKE cluster name. Auto-generated if not specified.
	ClusterName string
	// The number of nodes (default: 1).
	NodeCount int32
	// The GCE machine type (default: "e2-standard-4").
	MachineType string
	// Boot disk size in GB (default: 100).
	DiskSizeGB int32
	// Boot disk type: "pd-standard", "pd-ssd", or "pd-balanced" (default: "pd-standard").
	DiskType string
	// Kubernetes version. Uses GKE default if unspecified.
	KubernetesVersion string
	// Go duration format for long running operations.
	// Default: "30m".
	Timeout string
	// Resource labels to apply to the cluster.
	Tags       map[string]string
	Registries map[string]*RegistryConfig
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

// NewDriver creates a new GKE driver instance that uses the Google Cloud SDK
// to provision and manage a GKE cluster for running tests.
func NewDriver(name string, opts Options) (drivers.Tester, error) {
	k := &driver{
		name:              name,
		stack:             harness.NewStack(),
		region:            opts.Region,
		zone:              opts.Zone,
		clusterName:       opts.ClusterName,
		nodeCount:         opts.NodeCount,
		machineType:       opts.MachineType,
		diskSizeGB:        opts.DiskSizeGB,
		diskType:          opts.DiskType,
		kubernetesVersion: opts.KubernetesVersion,
		tags:              opts.Tags,
	}

	// Resolve project ID: explicit Terraform value first, then env-var fallbacks.
	k.projectID = resolveProjectID(context.Background(), opts.Project)
	if k.projectID == "" {
		return nil, fmt.Errorf("no GCP project ID specified: set the `project` field in Terraform, or GOOGLE_CLOUD_PROJECT in the environment")
	}

	// Validate location (must specify either region or zone, not both)
	if k.region == "" && k.zone == "" {
		k.region = regionDefault
	}
	if k.region != "" && k.zone != "" {
		return nil, fmt.Errorf("cannot specify both region and zone, choose one")
	}

	// Set defaults
	if k.nodeCount <= 0 {
		k.nodeCount = nodeCountDefault
	}
	if k.machineType == "" {
		k.machineType = machineTypeDefault
	}
	if k.diskSizeGB <= 0 {
		k.diskSizeGB = diskSizeGBDefault
	}
	if k.diskType == "" {
		k.diskType = diskTypeDefault
	}

	k.timeout = timeoutDefault
	if opts.Timeout != "" {
		timeout, err := time.ParseDuration(opts.Timeout)
		if err != nil {
			return nil, fmt.Errorf("unable to parse timeout setting: %s %v", opts.Timeout, err)
		}
		k.timeout = timeout
	}

	if opts.Registries != nil {
		k.registries = opts.Registries
	}

	return k, nil
}

func (k *driver) setupCommonClients(ctx context.Context) error {
	// Create GKE cluster client using Application Default Credentials
	clusterClient, err := container.NewClusterManagerClient(ctx)
	if err != nil {
		return fmt.Errorf("unable to create GKE cluster client: %w", err)
	}
	k.clusterClient = clusterClient

	return nil
}

func (k *driver) Setup(ctx context.Context) error {
	log := clog.FromContext(ctx)
	span := trace.SpanFromContext(ctx)

	// Initialize GCP clients
	if err := k.setupCommonClients(ctx); err != nil {
		return err
	}

	// Determine cluster name (env var or generate)
	existingCluster := false
	if n, ok := os.LookupEnv("IMAGETEST_GKE_CLUSTER"); ok {
		log.Infof("Using cluster name from IMAGETEST_GKE_CLUSTER: %s", n)
		k.clusterName = n
		existingCluster = true
	} else {
		if k.clusterName == "" {
			k.clusterName = "imagetest-" + uuid.New().String()[:8]
		}
		log.Infof("Using cluster name: %s", k.clusterName)
	}

	// Create temp kubeconfig file
	cfg, err := os.Create(filepath.Join(os.TempDir(), k.clusterName))
	if err != nil {
		return fmt.Errorf("creating temp kubeconfig: %w", err)
	}
	log.Infof("Using kubeconfig: %s", cfg.Name())
	k.kubeconfig = cfg.Name()

	// Create cluster (or skip if existing)
	if !existingCluster {
		if err = k.createCluster(ctx); err != nil {
			return err
		}
		span.AddEvent("gke.cluster.created")
	}

	// Get kubeconfig and create Kubernetes client
	if err = k.getKubeConfig(ctx); err != nil {
		return err
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

func (k *driver) createCluster(ctx context.Context) error {
	log := clog.FromContext(ctx)

	// Determine parent (regional or zonal)
	var parent string
	if k.region != "" {
		parent = fmt.Sprintf("projects/%s/locations/%s", k.projectID, k.region)
		log.Infof("Creating regional cluster in %s", k.region)
	} else {
		parent = fmt.Sprintf("projects/%s/locations/%s", k.projectID, k.zone)
		log.Infof("Creating zonal cluster in %s", k.zone)
	}

	// Build cluster request
	req := &containerpb.CreateClusterRequest{
		Parent: parent,
		Cluster: &containerpb.Cluster{
			Name:             k.clusterName,
			InitialNodeCount: k.nodeCount,
			NodeConfig: &containerpb.NodeConfig{
				MachineType: k.machineType,
				DiskSizeGb:  k.diskSizeGB,
				DiskType:    k.diskType,
				OauthScopes: []string{
					"https://www.googleapis.com/auth/cloud-platform",
				},
			},
			ResourceLabels: k.buildLabels(),
		},
	}

	// Set Kubernetes version if specified
	if k.kubernetesVersion != "" {
		req.Cluster.InitialClusterVersion = k.kubernetesVersion
	}

	log.Info("Creating GKE cluster",
		"name", k.clusterName,
		"project", k.projectID,
		"location", k.getLocation(),
		"node_count", k.nodeCount,
		"machine_type", k.machineType,
		"disk_size_gb", k.diskSizeGB,
		"disk_type", k.diskType,
	)

	// Start cluster creation (async operation)
	op, err := k.clusterClient.CreateCluster(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to initiate GKE cluster creation: %w", err)
	}

	// Register cleanup IMMEDIATELY. teardownCluster honors skip-teardown env
	// vars internally, so non-cluster stack items aren't short-circuited
	// when cluster teardown is skipped.
	if err := k.stack.Add(k.teardownCluster); err != nil {
		return err
	}

	// Wait for operation to complete
	log.Info("Waiting for GKE cluster provisioning...")

	// The operation name should be in format: projects/{project}/locations/{location}/operations/{operation}
	opName := op.GetName()

	// If the operation name doesn't include the full path, construct it
	if !strings.Contains(opName, "projects/") {
		opName = fmt.Sprintf("%s/operations/%s", parent, opName)
	}

	// Bound the polling loop by k.timeout. The select on ctx.Done() lets
	// callers cancel the wait immediately rather than waiting up to a full
	// pollFrequency tick.
	ctx, cancel := context.WithTimeout(ctx, k.timeout)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			if ctx.Err() == context.DeadlineExceeded {
				return fmt.Errorf("cluster creation timed out after %v", k.timeout)
			}
			return fmt.Errorf("cluster creation: %w", ctx.Err())
		case <-time.After(pollFrequency):
		}

		opReq := &containerpb.GetOperationRequest{
			Name: opName,
		}
		opResp, err := k.clusterClient.GetOperation(ctx, opReq)
		if err != nil {
			// If operation not found, check if cluster was created successfully
			// (operation might have completed and been cleaned up).
			if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "NotFound") {
				log.Info("Operation not found, checking if cluster was created successfully...")

				clusterReq := &containerpb.GetClusterRequest{
					Name: fmt.Sprintf("%s/clusters/%s", parent, k.clusterName),
				}
				cluster, clusterErr := k.clusterClient.GetCluster(ctx, clusterReq)
				if clusterErr == nil && cluster.Status == containerpb.Cluster_RUNNING {
					log.Infof("Created GKE cluster: %s (operation completed)", k.clusterName)
					return nil
				}

				return fmt.Errorf("failed to check operation status: %w", err)
			}
			return fmt.Errorf("failed to check operation status: %w", err)
		}

		switch opResp.Status {
		case containerpb.Operation_DONE:
			if e := opResp.GetError(); e != nil {
				return fmt.Errorf("cluster creation failed: %s", e.GetMessage())
			}
			log.Infof("Created GKE cluster: %s", k.clusterName)
			return nil
		case containerpb.Operation_ABORTING:
			return fmt.Errorf("cluster creation aborting: %s", opResp.GetError().GetMessage())
		case containerpb.Operation_PENDING, containerpb.Operation_RUNNING:
			log.Debugf("Cluster creation in progress, status: %s", opResp.Status)
		default:
			return fmt.Errorf("unexpected operation status: %v", opResp.Status)
		}
	}
}

func (k *driver) deleteCluster(ctx context.Context) error {
	log := clog.FromContext(ctx)

	parent := k.getParent()
	clusterName := fmt.Sprintf("%s/clusters/%s", parent, k.clusterName)

	log.Info("Initiating GKE cluster deletion", "cluster", clusterName)

	req := &containerpb.DeleteClusterRequest{
		Name: clusterName,
	}

	op, err := k.clusterClient.DeleteCluster(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to initiate cluster deletion: %w", err)
	}

	// Wait for deletion to complete
	log.Info("Waiting for cluster deletion...")

	// The operation name should be in format: projects/{project}/locations/{location}/operations/{operation}
	opName := op.GetName()

	// If the operation name doesn't include the full path, construct it
	if !strings.Contains(opName, "projects/") {
		opName = fmt.Sprintf("%s/operations/%s", parent, opName)
	}

	// Bound the polling loop by k.timeout. The select on ctx.Done() lets
	// callers cancel the wait immediately rather than waiting up to a full
	// pollFrequency tick.
	ctx, cancel := context.WithTimeout(ctx, k.timeout)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			if ctx.Err() == context.DeadlineExceeded {
				return fmt.Errorf("cluster deletion timed out after %v", k.timeout)
			}
			return fmt.Errorf("cluster deletion: %w", ctx.Err())
		case <-time.After(pollFrequency):
		}

		opReq := &containerpb.GetOperationRequest{
			Name: opName,
		}
		opResp, err := k.clusterClient.GetOperation(ctx, opReq)
		if err != nil {
			// If operation not found, verify cluster is really deleted.
			if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "NotFound") {
				log.Info("Operation not found, verifying cluster deletion...")

				clusterReq := &containerpb.GetClusterRequest{
					Name: fmt.Sprintf("%s/clusters/%s", parent, k.clusterName),
				}
				_, clusterErr := k.clusterClient.GetCluster(ctx, clusterReq)
				if clusterErr != nil && (strings.Contains(clusterErr.Error(), "not found") || strings.Contains(clusterErr.Error(), "NotFound")) {
					log.Info("Cluster deleted successfully (verified)")
					return nil
				}

				// Cluster still exists; loop back and poll again.
				log.Debug("Cluster still exists, continuing to wait...")
				continue
			}
			return fmt.Errorf("failed to check deletion status: %w", err)
		}

		switch opResp.Status {
		case containerpb.Operation_DONE:
			log.Info("Cluster deleted successfully")
			return nil
		case containerpb.Operation_ABORTING:
			return fmt.Errorf("cluster deletion aborting: %s", opResp.GetError().GetMessage())
		case containerpb.Operation_PENDING, containerpb.Operation_RUNNING:
			log.Debugf("Cluster deletion in progress, status: %s", opResp.Status)
		default:
			return fmt.Errorf("unexpected operation status: %v", opResp.Status)
		}
	}
}

func (k *driver) getKubeConfig(ctx context.Context) error {
	log := clog.FromContext(ctx)

	parent := k.getParent()
	clusterName := fmt.Sprintf("%s/clusters/%s", parent, k.clusterName)

	log.Infof("Retrieving cluster details: %s", clusterName)

	req := &containerpb.GetClusterRequest{
		Name: clusterName,
	}

	cluster, err := k.clusterClient.GetCluster(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to get cluster: %w", err)
	}

	if cluster.Endpoint == "" {
		return fmt.Errorf("cluster endpoint is empty")
	}

	// Decode CA certificate
	caCert, err := base64.StdEncoding.DecodeString(cluster.MasterAuth.ClusterCaCertificate)
	if err != nil {
		return fmt.Errorf("failed to decode CA certificate: %w", err)
	}

	// Construct kubeconfig
	// Note: This uses gke-gcloud-auth-plugin for authentication
	kubeconfig := clientcmdapi.Config{
		APIVersion: "v1",
		Kind:       "Config",
		Clusters: map[string]*clientcmdapi.Cluster{
			k.clusterName: {
				Server:                   fmt.Sprintf("https://%s", cluster.Endpoint),
				CertificateAuthorityData: caCert,
			},
		},
		Contexts: map[string]*clientcmdapi.Context{
			k.clusterName: {
				Cluster:  k.clusterName,
				AuthInfo: k.clusterName,
			},
		},
		CurrentContext: k.clusterName,
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			k.clusterName: {
				Exec: &clientcmdapi.ExecConfig{
					APIVersion:  "client.authentication.k8s.io/v1beta1",
					Command:     "gke-gcloud-auth-plugin",
					InstallHint: "Install gke-gcloud-auth-plugin for use with kubectl by following https://cloud.google.com/kubernetes-engine/docs/how-to/cluster-access-for-kubectl#install_plugin",
				},
			},
		},
	}

	// Write kubeconfig to file
	data, err := clientcmd.Write(kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to marshal kubeconfig: %w", err)
	}

	if err := os.WriteFile(k.kubeconfig, data, 0o644); err != nil {
		return fmt.Errorf("failed to write kubeconfig: %w", err)
	}

	log.Infof("Wrote kubeconfig to %s", k.kubeconfig)
	return nil
}

func (k *driver) Teardown(ctx context.Context) error {
	clog.FromContext(ctx).Info("Initiating resource teardown")

	// Create fresh context with timeout (the original may already be cancelled
	// by the time the test framework calls Teardown).
	teardownCtx, cancel := context.WithTimeout(context.Background(), k.timeout)
	defer cancel()

	// Use stack for LIFO cleanup. Each registered teardown function honors its
	// own skip-teardown logic — see teardownCluster.
	return k.stack.Teardown(teardownCtx)
}

// teardownCluster is the stack callback that deletes the GKE cluster. Honors
// the per-driver and global skip-teardown env vars (and the use-existing-
// cluster mode) so that non-cluster stack items can still run independently
// when the user wants to keep the cluster around for debugging.
func (k *driver) teardownCluster(ctx context.Context) error {
	log := clog.FromContext(ctx)

	if os.Getenv("IMAGETEST_GKE_SKIP_TEARDOWN") == "true" ||
		os.Getenv("IMAGETEST_SKIP_TEARDOWN") == "true" {
		log.Info("Skipping GKE cluster teardown")
		return nil
	}

	if _, ok := os.LookupEnv("IMAGETEST_GKE_CLUSTER"); ok {
		log.Info("Skipping cluster teardown for existing cluster")
		return nil
	}

	return k.deleteCluster(ctx)
}

func (k *driver) Run(ctx context.Context, ref name.Reference) (*drivers.RunResult, error) {
	log := clog.FromContext(ctx)
	log.Infof("Running test with image: %s", ref.String())

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

	// Delegate to shared pod.Run()
	return pod.Run(ctx, k.kcfg,
		pod.WithImageRef(ref),
		pod.WithExtraEnvs(map[string]string{
			"IMAGETEST_DRIVER": "gke",
		}),
		pod.WithRegistryStaticAuth(dcfg),
	)
}

func (k *driver) getLocation() string {
	if k.region != "" {
		return k.region
	}
	return k.zone
}

func (k *driver) getParent() string {
	return fmt.Sprintf("projects/%s/locations/%s", k.projectID, k.getLocation())
}

func (k *driver) buildLabels() map[string]string {
	labels := map[string]string{
		"imagetest":              "true",
		"imagetest-test-name":    sanitizeGCPLabel(k.name),
		"imagetest-cluster-name": sanitizeGCPLabel(k.clusterName),
	}
	for tagK, tagV := range k.tags {
		labels[sanitizeGCPLabel(tagK)] = sanitizeGCPLabel(tagV)
	}
	return labels
}

// resolveProjectID returns the GCP project ID, preferring the explicit value
// from the Terraform configuration, then falling back through the canonical
// GCP environment variables. Returns the empty string if no source is set;
// callers should surface a clear error when the result is empty.
//
// Resolution order:
//  1. explicit (the Terraform schema's `project` attribute, if set)
//  2. GOOGLE_CLOUD_PROJECT — the canonical project-ID env var used by the
//     official Go client libraries
//     (cloud.google.com/go/auth/internal/internal.go: projectEnvVar)
//  3. GOOGLE_PROJECT_ID — historical, non-standard, kept as a deprecated
//     fallback to avoid breaking existing smoke-test setups; emits a warning
//     when used
//
// Whitespace is trimmed at every source so that values like
// "$(cat /tmp/proj)" with trailing newlines work correctly.
func resolveProjectID(ctx context.Context, explicit string) string {
	if v := strings.TrimSpace(explicit); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("GOOGLE_CLOUD_PROJECT")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("GOOGLE_PROJECT_ID")); v != "" {
		clog.FromContext(ctx).Warn("GOOGLE_PROJECT_ID is deprecated; use GOOGLE_CLOUD_PROJECT instead")
		return v
	}
	return ""
}

// sanitizeGCPLabel sanitizes a string to be a valid GCP resource label key or
// value. GCP labels allow lowercase letters, digits, underscores, and dashes;
// max length is 63 characters. Invalid characters are replaced with a dash so
// that semantically distinct inputs (e.g. "image:test/foo") don't collapse to
// the same sanitized output. GCP additionally requires keys to begin with a
// lowercase letter — that constraint is intentionally not enforced here, since
// rewriting a user-provided key (e.g. by prefixing a letter) could cause
// collisions; callers are expected to provide keys that already start with a
// letter, and GCP will reject keys that don't.
//
// Reference: https://cloud.google.com/compute/docs/labeling-resources
func sanitizeGCPLabel(s string) string {
	s = strings.ToLower(s)
	s = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		switch r {
		case '_', '-':
			return r
		}
		return '-'
	}, s)
	if len(s) > 63 {
		s = s[:63]
	}
	return s
}
