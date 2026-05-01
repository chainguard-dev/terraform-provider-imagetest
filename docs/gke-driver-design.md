# GKE Driver Design Document

## Overview

This document outlines the design and implementation plan for adding a Google Kubernetes Engine (GKE) driver to terraform-provider-imagetest. The GKE driver will enable running container image tests on GKE clusters, following the same patterns established by the existing AKS and EKS drivers.

## Motivation

The terraform-provider-imagetest currently supports testing on AKS and EKS, but lacks GCP/GKE support. Adding a GKE driver provides:

- **Multi-cloud parity**: Support all three major cloud Kubernetes platforms (AWS EKS, Azure AKS, GCP GKE)
- **GCP-specific testing**: Enable testing of GCP-specific features (Workload Identity, GKE Autopilot, custom GKE node images)
- **Customer demand**: GCP users need to test images in their target deployment environment

## Architecture

### High-Level Design

The GKE driver follows the same architectural pattern as the AKS driver:

1. **Native SDK approach**: Use Google Cloud Go SDK directly (not `gcloud` CLI)
2. **Stack-based cleanup**: Use `harness.Stack` for reliable LIFO resource teardown
3. **Shared pod execution**: Reuse `pod.Run()` from `internal/drivers/pod/`
4. **Single-phase provisioning**: Create cluster with node pool in one API call
5. **Ambient authentication**: Use Application Default Credentials (ADC)

### Comparison with Existing Drivers

| Aspect | EKS | AKS | **GKE** (proposed) |
|--------|-----|-----|-------------------|
| Provisioning | eksctl CLI | Azure SDK | **Google Cloud SDK** |
| Cleanup | Manual LIFO | Stack pattern | **Stack pattern** |
| Node creation | 2-phase (cluster → nodegroup) | Single-phase | **Single-phase** |
| Kubeconfig | eksctl command | API call | **Extract from cluster** |
| Location model | Region only | Location (region) | **Region OR zone** |
| Tags | `map[string]string` | `map[string]*string` | **`map[string]string`** |
| Workload Identity | Pod Identity addon | UAI + Federated Creds | **GSA + IAM binding** |
| Registry integration | Static auth | ACR attachment | **GAR attachment** |

**Decision**: Follow the **AKS pattern** (native SDK + Stack) rather than EKS (CLI-based).

**Rationale**:
- GKE SDK is comprehensive and well-maintained
- Stack-based cleanup is more robust for partial failures
- Single-phase cluster creation is simpler
- Aligns with how GCP users interact with GKE (SDK/Terraform, not CLI)

## Implementation

### File Structure

```
internal/drivers/gke/
└── driver.go           # ~800-1000 lines (all in one file, like AKS)
```

Single-file approach maintains consistency with AKS driver.

### Core Types

```go
package gke

import (
    container "cloud.google.com/go/container/apiv1"
    "cloud.google.com/go/container/apiv1/containerpb"
    "github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
    "github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
    "k8s.io/client-go/kubernetes"
    "k8s.io/client-go/rest"
)

type driver struct {
    name  string
    stack *harness.Stack

    // Configuration
    projectID         string
    region            string  // Regional cluster (preferred)
    zone              string  // Zonal cluster (alternative)
    clusterName       string
    nodeCount         int32
    machineType       string
    diskSizeGB        int32
    diskType          string
    kubernetesVersion string
    tags              map[string]string

    // Runtime state
    kubeconfig string
    kcli       kubernetes.Interface
    kcfg       *rest.Config

    // GCP SDK clients
    clusterClient *container.ClusterManagerClient

    // Features
    workloadIdentityAssociations []*WorkloadIdentityAssociationOptions
    attachedGARs                 []*AttachedGAR
    registries                   map[string]*RegistryConfig
}

type Options struct {
    Project           string
    Region            string  // Regional cluster (e.g., "us-central1")
    Zone              string  // Zonal cluster (e.g., "us-central1-a")
    ClusterName       string  // Optional, auto-generated if empty
    NodeCount         int32
    MachineType       string
    DiskSizeGB        int32
    DiskType          string  // "pd-standard", "pd-ssd", "pd-balanced"
    KubernetesVersion string
    Tags              map[string]string
    Timeout           string

    WorkloadIdentityAssociations []*WorkloadIdentityAssociationOptions
    AttachedGARs                 []*AttachedGAR
    Registries                   map[string]*RegistryConfig
}

// Workload Identity: binds GCP Service Account to K8s Service Account
type WorkloadIdentityAssociationOptions struct {
    ServiceAccountName string  // K8s SA name
    Namespace          string  // K8s namespace
    GSAEmail           string  // GCP service account email
    IAMRoles           []string // IAM roles to grant (e.g., "roles/storage.objectViewer")
}

// Artifact Registry attachment
type AttachedGAR struct {
    Repository      string  // Format: "projects/PROJECT/locations/LOCATION/repositories/REPO"
    CreateIfMissing bool
}

type RegistryConfig struct {
    Auth *RegistryAuthConfig
}

type RegistryAuthConfig struct {
    Username string
    Password string
    Auth     string
}
```

### Key Methods

```go
// NewDriver creates a new GKE driver instance
func NewDriver(name string, opts Options) (drivers.Tester, error)

// Setup creates the GKE cluster and configures access
func (k *driver) Setup(ctx context.Context) error

// Run executes a test container using the shared pod.Run() function
func (k *driver) Run(ctx context.Context, ref name.Reference) (*drivers.RunResult, error)

// Teardown destroys all created resources using the stack
func (k *driver) Teardown(ctx context.Context) error

// Internal methods
func (k *driver) setupCommonClients(ctx context.Context) error
func (k *driver) createCluster(ctx context.Context) error
func (k *driver) deleteCluster(ctx context.Context) error
func (k *driver) getKubeConfig(ctx context.Context) error
func (k *driver) createWorkloadIdentity(ctx context.Context) error
func (k *driver) attachGARs(ctx context.Context) error
func (k *driver) buildLabels() map[string]string
```

### Setup Flow

```go
func (k *driver) Setup(ctx context.Context) error {
    log := clog.FromContext(ctx)
    span := trace.SpanFromContext(ctx)

    // 1. Initialize GCP clients (Application Default Credentials)
    if err := k.setupCommonClients(ctx); err != nil {
        return err
    }

    // 2. Determine cluster name (env var or generate)
    existingCluster := false
    if n, ok := os.LookupEnv("IMAGETEST_GKE_CLUSTER"); ok {
        log.Infof("Using cluster name from IMAGETEST_GKE_CLUSTER: %s", n)
        k.clusterName = n
        existingCluster = true
    } else {
        k.clusterName = "imagetest-" + uuid.New().String()[:8]
        log.Infof("Using random cluster name: %s", k.clusterName)
    }

    // 3. Create temp kubeconfig file
    cfg, err := os.Create(filepath.Join(os.TempDir(), k.clusterName))
    if err != nil {
        return fmt.Errorf("creating temp kubeconfig: %w", err)
    }
    k.kubeconfig = cfg.Name()

    // 4. Create cluster (or skip if existing)
    if !existingCluster {
        if err = k.createCluster(ctx); err != nil {
            return err
        }
        span.AddEvent("gke.cluster.created")
    }

    // 5. Configure Workload Identity
    if err = k.createWorkloadIdentity(ctx); err != nil {
        return err
    }
    span.AddEvent("gke.identity.configured")

    // 6. Attach Artifact Registries
    if err = k.attachGARs(ctx); err != nil {
        return err
    }
    span.AddEvent("gke.gar.attached")

    // 7. Get kubeconfig and create Kubernetes client
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
```

### Cluster Creation

```go
func (k *driver) createCluster(ctx context.Context) error {
    log := clog.FromContext(ctx)

    // Determine parent (regional or zonal)
    var parent string
    if k.region != "" {
        parent = fmt.Sprintf("projects/%s/locations/%s", k.projectID, k.region)
        log.Infof("Creating regional cluster in %s", k.region)
    } else if k.zone != "" {
        parent = fmt.Sprintf("projects/%s/locations/%s", k.projectID, k.zone)
        log.Infof("Creating zonal cluster in %s", k.zone)
    } else {
        return fmt.Errorf("either region or zone must be specified")
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
            WorkloadIdentityConfig: &containerpb.WorkloadIdentityConfig{
                WorkloadPool: fmt.Sprintf("%s.svc.id.goog", k.projectID),
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
        "node_count", k.nodeCount,
        "machine_type", k.machineType,
        "disk_size_gb", k.diskSizeGB,
    )

    // Start cluster creation (async operation)
    op, err := k.clusterClient.CreateCluster(ctx, req)
    if err != nil {
        return fmt.Errorf("failed to initiate GKE cluster creation: %w", err)
    }

    // Register cleanup IMMEDIATELY
    if err := k.stack.Add(func(ctx context.Context) error {
        return k.deleteCluster(ctx)
    }); err != nil {
        return err
    }

    // Wait for operation to complete
    log.Info("Waiting for GKE cluster provisioning...")
    for {
        opResp, err := k.clusterClient.GetOperation(ctx, &containerpb.GetOperationRequest{
            Name: op.Name,
        })
        if err != nil {
            return fmt.Errorf("failed to check operation status: %w", err)
        }

        if opResp.Status == containerpb.Operation_DONE {
            if opResp.Error != nil {
                return fmt.Errorf("cluster creation failed: %s", opResp.Error.Message)
            }
            log.Infof("Created GKE cluster: %s", k.clusterName)
            return nil
        }

        time.Sleep(pollFrequency)
    }
}
```

### Kubeconfig Retrieval

Unlike AKS (separate API call) or EKS (CLI command), GKE requires extracting credentials from the cluster object and constructing the kubeconfig:

```go
func (k *driver) getKubeConfig(ctx context.Context) error {
    // Get cluster details
    parent := fmt.Sprintf("projects/%s/locations/%s", k.projectID, k.getLocation())
    clusterName := fmt.Sprintf("%s/clusters/%s", parent, k.clusterName)
    
    cluster, err := k.clusterClient.GetCluster(ctx, &containerpb.GetClusterRequest{
        Name: clusterName,
    })
    if err != nil {
        return fmt.Errorf("failed to get cluster: %w", err)
    }

    // Construct kubeconfig
    kubeconfig := clientcmdapi.Config{
        APIVersion: "v1",
        Kind:       "Config",
        Clusters: map[string]*clientcmdapi.Cluster{
            k.clusterName: {
                Server:                   fmt.Sprintf("https://%s", cluster.Endpoint),
                CertificateAuthorityData: []byte(cluster.MasterAuth.ClusterCaCertificate),
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
                    APIVersion: "client.authentication.k8s.io/v1beta1",
                    Command:    "gke-gcloud-auth-plugin",
                    InstallHint: "Install gke-gcloud-auth-plugin for kubectl authentication",
                },
            },
        },
    }

    // Write kubeconfig to file
    data, err := clientcmd.Write(kubeconfig)
    if err != nil {
        return fmt.Errorf("failed to marshal kubeconfig: %w", err)
    }

    if err := os.WriteFile(k.kubeconfig, data, 0644); err != nil {
        return fmt.Errorf("failed to write kubeconfig: %w", err)
    }

    return nil
}

func (k *driver) getLocation() string {
    if k.region != "" {
        return k.region
    }
    return k.zone
}
```

### Run Implementation

Identical to AKS and EKS:

```go
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

    // Delegate to shared pod.Run()
    return pod.Run(ctx, k.kcfg,
        pod.WithImageRef(ref),
        pod.WithExtraEnvs(map[string]string{
            "IMAGETEST_DRIVER": "gke",
        }),
        pod.WithRegistryStaticAuth(dcfg),
    )
}
```

### Teardown Implementation

Identical to AKS:

```go
func (k *driver) Teardown(ctx context.Context) error {
    log := clog.FromContext(ctx)
    
    // Check skip teardown flags
    if os.Getenv("IMAGETEST_GKE_SKIP_TEARDOWN") == "true" ||
       os.Getenv("IMAGETEST_SKIP_TEARDOWN") == "true" {
        log.Info("Skipping GKE teardown")
        return nil
    }

    // Skip if using existing cluster
    if _, ok := os.LookupEnv("IMAGETEST_GKE_CLUSTER"); ok {
        log.Info("Skipping teardown for existing cluster")
        return nil
    }

    log.Info("Initiating resource teardown")

    // Create fresh context with timeout (original may be cancelled)
    teardownCtx, cancel := context.WithTimeout(context.Background(), k.timeout)
    defer cancel()

    // Use stack for LIFO cleanup
    return k.stack.Teardown(teardownCtx)
}
```

## Provider Integration

### Constants and Models

In `internal/provider/drivers.go`:

```go
const (
    // ...
    DriverGKE DriverResourceModel = "gke"
)

type TestsDriversResourceModel struct {
    // ...
    GKE *GKEDriverResourceModel `tfsdk:"gke"`
}

type GKEDriverResourceModel struct {
    Project           types.String  `tfsdk:"project"`
    Region            types.String  `tfsdk:"region"`
    Zone              types.String  `tfsdk:"zone"`
    ClusterName       types.String  `tfsdk:"cluster_name"`
    NodeCount         types.Int32   `tfsdk:"node_count"`
    MachineType       types.String  `tfsdk:"machine_type"`
    DiskSizeGB        types.Int32   `tfsdk:"disk_size_gb"`
    DiskType          types.String  `tfsdk:"disk_type"`
    KubernetesVersion types.String  `tfsdk:"kubernetes_version"`
    Tags              map[string]string `tfsdk:"tags"`
    
    WorkloadIdentityAssociations []*GKEWorkloadIdentityAssociationResourceModel `tfsdk:"workload_identity_associations"`
    AttachedGARs                 []*GKEAttachedGAR `tfsdk:"attached_gars"`
}

type GKEWorkloadIdentityAssociationResourceModel struct {
    ServiceAccountName types.String   `tfsdk:"service_account_name"`
    Namespace          types.String   `tfsdk:"namespace"`
    GSAEmail           types.String   `tfsdk:"gsa_email"`
    IAMRoles           []types.String `tfsdk:"iam_roles"`
}

type GKEAttachedGAR struct {
    Repository      types.String `tfsdk:"repository"`
    CreateIfMissing types.Bool   `tfsdk:"create_if_missing"`
}
```

### LoadDriver Case

```go
case DriverGKE:
    cfg := driversCfg.GKE
    if cfg == nil {
        cfg = &GKEDriverResourceModel{}
    }

    // Build registry auth (same pattern as AKS/EKS)
    registries := make(map[string]*gke.RegistryConfig)
    r, err := name.NewRegistry(repo.RegistryStr())
    if err != nil {
        return nil, fmt.Errorf("invalid registry name %s: %w", repo.RegistryStr(), err)
    }
    a, err := authn.DefaultKeychain.Resolve(r)
    if err != nil {
        return nil, fmt.Errorf("resolving keychain for registry %s: %w", r.String(), err)
    }
    acfg, err := a.Authorization()
    if err != nil {
        return nil, fmt.Errorf("getting authorization for registry %s: %w", r.String(), err)
    }
    registries[repo.RegistryStr()] = &gke.RegistryConfig{
        Auth: &gke.RegistryAuthConfig{
            Username: acfg.Username,
            Password: acfg.Password,
            Auth:     acfg.Auth,
        },
    }

    // Convert workload identity associations
    workloadIdentityAssociations := []*gke.WorkloadIdentityAssociationOptions{}
    if cfg.WorkloadIdentityAssociations != nil {
        for _, v := range cfg.WorkloadIdentityAssociations {
            association := &gke.WorkloadIdentityAssociationOptions{
                ServiceAccountName: v.ServiceAccountName.ValueString(),
                Namespace:          v.Namespace.ValueString(),
                GSAEmail:           v.GSAEmail.ValueString(),
            }
            for _, role := range v.IAMRoles {
                association.IAMRoles = append(association.IAMRoles, role.ValueString())
            }
            workloadIdentityAssociations = append(workloadIdentityAssociations, association)
        }
    }

    // Convert attached GARs
    attachedGARs := []*gke.AttachedGAR{}
    if cfg.AttachedGARs != nil {
        for _, v := range cfg.AttachedGARs {
            attachedGARs = append(attachedGARs, &gke.AttachedGAR{
                Repository:      v.Repository.ValueString(),
                CreateIfMissing: v.CreateIfMissing.ValueBool(),
            })
        }
    }

    return gke.NewDriver(id, gke.Options{
        Project:                      cfg.Project.ValueString(),
        Region:                       cfg.Region.ValueString(),
        Zone:                         cfg.Zone.ValueString(),
        ClusterName:                  cfg.ClusterName.ValueString(),
        NodeCount:                    cfg.NodeCount.ValueInt32(),
        MachineType:                  cfg.MachineType.ValueString(),
        DiskSizeGB:                   cfg.DiskSizeGB.ValueInt32(),
        DiskType:                     cfg.DiskType.ValueString(),
        KubernetesVersion:            cfg.KubernetesVersion.ValueString(),
        Tags:                         cfg.Tags,
        Timeout:                      timeout,
        Registries:                   registries,
        WorkloadIdentityAssociations: workloadIdentityAssociations,
        AttachedGARs:                 attachedGARs,
    })
```

### Terraform Schema

```go
"gke": schema.SingleNestedAttribute{
    Description: "The GKE driver",
    Optional:    true,
    Attributes: map[string]schema.Attribute{
        "project": schema.StringAttribute{
            Description: "The GCP project ID. Falls back to the GOOGLE_CLOUD_PROJECT environment variable, then the deprecated GOOGLE_PROJECT_ID env var.",
            Optional:    true,
        },
        "region": schema.StringAttribute{
            Description: "The GCP region for a regional cluster (e.g., 'us-central1'). Mutually exclusive with zone.",
            Optional:    true,
        },
        "zone": schema.StringAttribute{
            Description: "The GCP zone for a zonal cluster (e.g., 'us-central1-a'). Mutually exclusive with region.",
            Optional:    true,
        },
        "cluster_name": schema.StringAttribute{
            Description: "The GKE cluster name. Auto-generated if not specified.",
            Optional:    true,
        },
        "node_count": schema.Int32Attribute{
            Description: "The number of nodes (default: 1)",
            Optional:    true,
        },
        "machine_type": schema.StringAttribute{
            Description: "The GCE machine type (default: 'e2-standard-4')",
            Optional:    true,
        },
        "disk_size_gb": schema.Int32Attribute{
            Description: "Boot disk size in GB (default: 100)",
            Optional:    true,
        },
        "disk_type": schema.StringAttribute{
            Description: "Boot disk type: 'pd-standard', 'pd-ssd', or 'pd-balanced' (default: 'pd-standard')",
            Optional:    true,
        },
        "kubernetes_version": schema.StringAttribute{
            Description: "Kubernetes version. Uses GKE default if unspecified.",
            Optional:    true,
        },
        "tags": schema.MapAttribute{
            Description: "Resource labels to apply to the cluster.",
            ElementType: types.StringType,
            Optional:    true,
        },
        "workload_identity_associations": schema.ListNestedAttribute{
            Description: "Workload Identity bindings (GSA → KSA)",
            Optional:    true,
            NestedObject: schema.NestedAttributeObject{
                Attributes: map[string]schema.Attribute{
                    "service_account_name": schema.StringAttribute{
                        Description: "Kubernetes service account name",
                        Required:    true,
                    },
                    "namespace": schema.StringAttribute{
                        Description: "Kubernetes namespace",
                        Required:    true,
                    },
                    "gsa_email": schema.StringAttribute{
                        Description: "GCP service account email (e.g., 'sa@PROJECT.iam.gserviceaccount.com')",
                        Required:    true,
                    },
                    "iam_roles": schema.ListAttribute{
                        Description: "IAM roles to grant to the GSA (e.g., 'roles/storage.objectViewer')",
                        ElementType: types.StringType,
                        Optional:    true,
                    },
                },
            },
        },
        "attached_gars": schema.ListNestedAttribute{
            Description: "Artifact Registry repositories to grant pull access",
            Optional:    true,
            NestedObject: schema.NestedAttributeObject{
                Attributes: map[string]schema.Attribute{
                    "repository": schema.StringAttribute{
                        Description: "Repository path: 'projects/PROJECT/locations/LOCATION/repositories/REPO'",
                        Required:    true,
                    },
                    "create_if_missing": schema.BoolAttribute{
                        Description: "Create the repository if it doesn't exist",
                        Optional:    true,
                    },
                },
            },
        },
    },
},
```

## Entrypoint Integration

Add to `cmd/entrypoint/kodata/entrypoint-wrapper.sh`:

```bash
gke)
  init_gke "$cmd"
  ;;

# Initialize and manage a GKE environment.
# Arguments:
#   $1: Path to the test script (already validated)
init_gke() {
  cmd="$1"

  if which kubectl; then
    # Set a default context to better mimic a local setup
    kubectl config set-context default --cluster=kubernetes --user=default --namespace=default
    kubectl config use-context default

    # Ensure required environment variables are set
    if [ -z "${POD_NAME-}" ] || [ -z "${POD_NAMESPACE-}" ]; then
      error "POD_NAME and POD_NAMESPACE environment variables must be set"
      exit 1
    fi

    info "Waiting for pod ${POD_NAME} to be ready..."
    if ! kubectl wait --for=condition=Ready=true pod/"${POD_NAME}" -n "${POD_NAMESPACE}" --timeout=60s; then
      error "Pod ${POD_NAME} failed to become ready"
      exit 1
    fi
  else
    warn "kubectl missing, skipping readiness check"
  fi

  exec "$cmd"
}
```

## Testing

### Unit Tests

Location: `internal/drivers/gke/driver_test.go`

```go
package gke

import (
    "testing"
)

func TestNewDriver(t *testing.T) {
    tests := []struct {
        name    string
        opts    Options
        wantErr bool
    }{
        {
            name: "valid minimal config",
            opts: Options{
                Project: "test-project",
                Region:    "us-central1",
            },
            wantErr: false,
        },
        {
            name: "missing project ID",
            opts: Options{
                Region: "us-central1",
            },
            wantErr: true,
        },
        {
            name: "both region and zone specified",
            opts: Options{
                Project: "test-project",
                Region:    "us-central1",
                Zone:      "us-central1-a",
            },
            wantErr: true,
        },
        {
            name: "neither region nor zone specified",
            opts: Options{
                Project: "test-project",
            },
            wantErr: true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            _, err := NewDriver("test", tt.opts)
            if (err != nil) != tt.wantErr {
                t.Errorf("NewDriver() error = %v, wantErr %v", err, tt.wantErr)
            }
        })
    }
}
```

### Acceptance Tests

Location: `internal/provider/tests_resource_gke_test.go`

```go
//go:build gke

package provider

import (
    "fmt"
    "os"
    "testing"

    "github.com/hashicorp/terraform-plugin-framework/providerserver"
    "github.com/hashicorp/terraform-plugin-go/tfprotov6"
    "github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccTestsResource_GKE(t *testing.T) {
    projectID := os.Getenv("IMAGETEST_GKE_PROJECT")
    region := os.Getenv("IMAGETEST_GKE_REGION")
    if region == "" {
        region = "us-central1"
    }

    repo := "ttl.sh/imagetest"

    // Test 1: Basic cluster creation
    tf := fmt.Sprintf(`
resource "imagetest_tests" "foo" {
  name   = "foo"
  driver = "gke"

  drivers = {
    gke = {
      project = %q
      region     = %q
    }
  }

  images = {
    foo = "cgr.dev/chainguard/busybox:latest@sha256:c546e746013d75c1fc9bf01b7a645ce7caa1ec46c45cb618c6e28d7b57bccc85"
  }

  tests = [
    {
      name    = "basic"
      image   = "cgr.dev/chainguard/busybox:latest@sha256:ecc152fe3dece44e60d1aa0fbbefb624902b4af0e2ed8c2c84dfbce653ff064f"
      cmd     = "echo success"
    }
  ]

  timeout = "30m"
}
`, projectID, region)

    // Test 2: Custom node configuration
    tfWithCustomNodes := fmt.Sprintf(`
resource "imagetest_tests" "foo_custom" {
  name   = "foo-custom"
  driver = "gke"

  drivers = {
    gke = {
      project      = %q
      region       = %q
      node_count   = 2
      machine_type = "n1-standard-4"
      disk_size_gb = 150
      disk_type    = "pd-ssd"
      tags = {
        "team"        = "platform"
        "environment" = "test"
      }
    }
  }

  images = {
    foo = "cgr.dev/chainguard/busybox:latest@sha256:c546e746013d75c1fc9bf01b7a645ce7caa1ec46c45cb618c6e28d7b57bccc85"
  }

  tests = [
    {
      name    = "basic"
      image   = "cgr.dev/chainguard/busybox:latest@sha256:ecc152fe3dece44e60d1aa0fbbefb624902b4af0e2ed8c2c84dfbce653ff064f"
      cmd     = "echo success"
    }
  ]

  timeout = "30m"
}
`, projectID, region)

    // Test 3: Workload Identity
    gsaEmail := fmt.Sprintf("test-sa@%s.iam.gserviceaccount.com", projectID)
    tfWithWorkloadIdentity := fmt.Sprintf(`
resource "imagetest_tests" "foo_wi" {
  name   = "foo-wi"
  driver = "gke"

  drivers = {
    gke = {
      project = %q
      region     = %q
      
      workload_identity_associations = [
        {
          service_account_name = "default"
          namespace            = "default"
          gsa_email            = %q
          iam_roles = [
            "roles/storage.objectViewer"
          ]
        }
      ]
    }
  }

  images = {
    foo = "cgr.dev/chainguard/busybox:latest@sha256:c546e746013d75c1fc9bf01b7a645ce7caa1ec46c45cb618c6e28d7b57bccc85"
  }

  tests = [
    {
      name    = "basic"
      image   = "cgr.dev/chainguard/kubectl:latest-dev"
      cmd     = "kubectl get sa default -o yaml"
    }
  ]

  timeout = "30m"
}
`, projectID, region, gsaEmail)

    resource.Test(t, resource.TestCase{
        PreCheck: func() {
            testAccPreCheck(t)
            if projectID == "" {
                t.Fatal("IMAGETEST_GKE_PROJECT must be set for acceptance tests")
            }
        },
        ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
            "imagetest": providerserver.NewProtocol6WithError(&ImageTestProvider{
                repo: repo,
            }),
        },
        Steps: []resource.TestStep{
            {Config: tf},
            {Config: tfWithCustomNodes},
            {Config: tfWithWorkloadIdentity},
        },
    })
}
```

### Running Tests

```bash
# Unit tests
go test ./internal/drivers/gke/... -v

# Acceptance tests (requires GCP credentials)
export IMAGETEST_GKE_PROJECT="my-test-project"
export IMAGETEST_GKE_REGION="us-central1"
export TF_ACC=1
go test ./internal/provider/... -v -tags=gke -timeout=60m

# Or use make target
make testacc TESTARGS="-tags=gke -run=TestAccTestsResource_GKE"
```

## Environment Variables

| Variable | Purpose | Example |
|----------|---------|---------|
| `IMAGETEST_GKE_CLUSTER` | Use existing cluster (skip creation) | `my-test-cluster` |
| `IMAGETEST_GKE_SKIP_TEARDOWN` | Keep GKE resources after test | `true` |
| `IMAGETEST_SKIP_TEARDOWN` | Global skip teardown (all drivers) | `true` |
| `GOOGLE_CLOUD_PROJECT` | Default GCP project ID (canonical; checked by the official Go client libraries) | `my-project-123` |
| `GOOGLE_PROJECT_ID` | Default GCP project ID (**deprecated** — use `GOOGLE_CLOUD_PROJECT`) | `my-project-123` |
| `GOOGLE_APPLICATION_CREDENTIALS` | Path to service account key | `/path/to/sa.json` |

**Authentication priority**:
1. `GOOGLE_APPLICATION_CREDENTIALS` (service account key file)
2. `gcloud` CLI credentials (`gcloud auth application-default login`)
3. GCE metadata server (when running in GCP)

## Configuration Examples

### Minimal Configuration

```hcl
resource "imagetest_tests" "basic" {
  name   = "basic-test"
  driver = "gke"

  drivers = {
    gke = {
      project = "my-project"
      region     = "us-central1"
    }
  }

  images = {
    nginx = "cgr.dev/chainguard/nginx:latest"
  }

  tests = [{
    name  = "smoke-test"
    image = "cgr.dev/chainguard/kubectl:latest-dev"
    cmd   = "kubectl run nginx --image=$IMAGES[\"nginx\"] --dry-run=client -o yaml"
  }]
}
```

### Advanced Configuration

```hcl
resource "imagetest_tests" "advanced" {
  name   = "advanced-gke"
  driver = "gke"

  drivers = {
    gke = {
      project            = "my-project"
      region             = "us-central1"
      node_count         = 3
      machine_type       = "n2-standard-8"
      disk_size_gb       = 200
      disk_type          = "pd-ssd"
      kubernetes_version = "1.28"

      tags = {
        team        = "platform"
        environment = "ci"
        cost-center = "engineering"
      }

      # Workload Identity: bind K8s SA to GCP SA
      workload_identity_associations = [{
        service_account_name = "test-runner"
        namespace            = "default"
        gsa_email            = "test-runner@my-project.iam.gserviceaccount.com"
        iam_roles = [
          "roles/storage.objectViewer",
          "roles/secretmanager.secretAccessor"
        ]
      }]

      # Grant pull access to private Artifact Registry
      attached_gars = [{
        repository      = "projects/my-project/locations/us-central1/repositories/my-repo"
        create_if_missing = false
      }]
    }
  }

  images = {
    app = "us-central1-docker.pkg.dev/my-project/my-repo/my-app:latest"
  }

  tests = [{
    name  = "integration-test"
    image = "cgr.dev/chainguard/kubectl:latest-dev"
    content = [{
      source = "./tests/integration"
    }]
    cmd = "/imagetest/run-integration-tests.sh"
  }]

  timeout = "45m"
}
```

## Implementation Phases

### Phase 1: MVP (Core Functionality)
**Goal**: Basic GKE cluster creation and test execution

- [ ] Create `internal/drivers/gke/driver.go`
- [ ] Implement `NewDriver()` with validation
- [ ] Implement `setupCommonClients()`
- [ ] Implement `Setup()` flow
- [ ] Implement `createCluster()` with stack registration
- [ ] Implement `getKubeConfig()`
- [ ] Implement `Run()` (delegate to `pod.Run()`)
- [ ] Implement `Teardown()` (use stack)
- [ ] Add entrypoint wrapper case
- [ ] Provider integration (constants, models, LoadDriver, schema)
- [ ] Basic unit tests
- [ ] Basic acceptance test

**Estimated effort**: 2-3 days

### Phase 2: Advanced Features
**Goal**: Feature parity with AKS driver

- [ ] Workload Identity support
  - [ ] Bind GSA to KSA
  - [ ] Grant IAM roles
  - [ ] Annotate K8s SA with GSA email
- [ ] Artifact Registry attachment
  - [ ] Grant `artifactregistry.reader` role to node SA
  - [ ] Optional: Create repository if missing
- [ ] Enhanced error handling
  - [ ] Detect quota errors
  - [ ] Handle rate limiting
  - [ ] Better operation polling
- [ ] Comprehensive tests
  - [ ] Workload Identity test
  - [ ] GAR attachment test
  - [ ] Zonal cluster test

**Estimated effort**: 2-3 days

### Phase 3: Polish & Documentation
**Goal**: Production-ready

- [ ] Comprehensive documentation
  - [ ] User guide in docs/
  - [ ] Example Terraform configs
  - [ ] Troubleshooting guide
- [ ] CI/CD integration
  - [ ] Add GKE tests to GitHub Actions
  - [ ] Set up test project
  - [ ] Manage service account credentials
- [ ] Performance optimization
  - [ ] Parallel resource creation where possible
  - [ ] Optimize polling intervals
- [ ] Edge case handling
  - [ ] Cluster already exists
  - [ ] Partial cleanup on failure
  - [ ] Network connectivity issues

**Estimated effort**: 1-2 days

## Open Questions & Future Work

### Open Questions

1. **Authentication strategy**: Should we support `gke-gcloud-auth-plugin` in the kubeconfig, or use token-based auth directly in the SDK?
   - **Recommendation**: Use `gke-gcloud-auth-plugin` for better compatibility with existing kubectl tooling

2. **Regional vs Zonal**: Should we default to regional (HA) or zonal (cheaper)?
   - **Recommendation**: Default to regional for reliability, allow zonal as opt-in

3. **Autopilot support**: Should we support GKE Autopilot mode?
   - **Recommendation**: Phase 4 - separate feature after standard mode is stable

4. **Node image**: Should we support custom node images (similar to EKS nodeAMI)?
   - **Recommendation**: Yes, add `node_image` option for testing COS variants

### Future Enhancements

- **GKE Autopilot mode**: Fully managed nodes, no node configuration needed
- **Multi-cluster testing**: Support creating multiple clusters in one test
- **Private clusters**: VPC-native clusters with private endpoints
- **Shielded nodes**: Security hardening features
- **Binary Authorization**: Image signature verification
- **Custom node pools**: Multiple node pools with different machine types
- **Preemptible nodes**: Cost optimization for CI workloads
- **Release channels**: Follow GKE's Rapid/Regular/Stable channels

## Risk Mitigation

| Risk | Mitigation |
|------|------------|
| Cluster creation timeout (>15 min) | Increase default timeout to 30m, make configurable |
| Quota exhaustion in test project | Document quota requirements, implement exponential backoff |
| Authentication failures in CI | Provide clear error messages, document workload identity federation setup |
| Incomplete cleanup on failure | Use stack pattern, add automated cleanup job for orphaned resources |
| SDK breaking changes | Pin SDK version, test upgrades in separate branch |
| GKE API rate limiting | Implement retry logic with exponential backoff |

## Success Criteria

- [ ] MVP can create GKE cluster, run test, and clean up successfully
- [ ] Acceptance tests pass consistently (>95% success rate)
- [ ] Workload Identity feature works with GCS/Secret Manager
- [ ] Documentation is clear and includes working examples
- [ ] Code review approved by maintainers
- [ ] CI/CD pipeline includes GKE tests

## References

- [GKE Go SDK Documentation](https://pkg.go.dev/cloud.google.com/go/container/apiv1)
- [GKE REST API Reference](https://cloud.google.com/kubernetes-engine/docs/reference/rest)
- [GKE Workload Identity](https://cloud.google.com/kubernetes-engine/docs/how-to/workload-identity)
- [Artifact Registry IAM](https://cloud.google.com/artifact-registry/docs/access-control)
- [Application Default Credentials](https://cloud.google.com/docs/authentication/application-default-credentials)
- AKS driver implementation: `internal/drivers/aks/driver.go`
- EKS driver implementation: `internal/drivers/eks_with_eksctl/driver.go`
