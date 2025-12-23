package aks

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v8"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/docker"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers/pod"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
	"github.com/charmbracelet/log"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/uuid"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	locationDefault   = "westeurope"
	nodeCountDefault  = 1
	nodeVMSizeDefault = "Standard_DS2_v2"
	timeoutDefault    = 30 * time.Minute
	pollFrequency     = 30 * time.Second
	defaultPoolName   = "nodepool0"
)

type driver struct {
	name  string
	stack *harness.Stack

	resourceGroup     string
	location          string
	nodeCount         int32
	nodeVMSize        string
	nodeDiskSize      int32
	nodeDiskType      string
	nodePoolName      string
	timeout           time.Duration
	subscriptionID    string
	kubernetesVersion string
	tags              map[string]string

	clusterName string
	dnsPrefix   string

	kubeconfig string
	kcli       kubernetes.Interface
	kcfg       *rest.Config

	aksClient *armcontainerservice.ManagedClustersClient
	aksCred   azcore.TokenCredential

	registries map[string]*RegistryConfig

	podIdentityAssociations []*PodIdentityAssociationOptions
}

type Options struct {
	// REQUIRED. An existing Azure resource group that will hold the AKS
	// cluster resources.
	ResourceGroup string
	// Azure region.
	// Default: westeurope
	Location string
	// The AKS cluster node count.
	// Default: 1.
	NodeCount int32
	// The Azure VM size used by the AKS cluster nodes.
	// Default: "Standard_DS2_v2"
	NodeVMSize string
	// Use a custom VM disk size (GB) instead of the one defined by the VM size.
	NodeDiskSize int32
	// The disk type: "Ephemeral" or "Managed".
	// Defaults to "Ephemeral", which provide better performance but aren't persistent.
	NodeDiskType string
	// The node pool name.
	NodePoolName string
	// Go duration format for long running operations, such as AKS cluster
	// provisioning.
	// Default: "20m".
	Timeout string
	// The Azure subscription ID.
	// Defaults to the "AZURE_SUBSCRIPTION_ID" environment value.
	SubscriptionID string
	// The Kubernetes version to deploy.
	// Uses the Azure default if unspecified.
	KubernetesVersion string
	Tags              map[string]string
	// DNS prefix. Defaults to the cluster name.
	DNSPrefix string

	Registries map[string]*RegistryConfig

	PodIdentityAssociations []*PodIdentityAssociationOptions
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

type PodIdentityAssociationOptions struct {
	ServiceAccountName string
	Namespace          string
	RoleAssignments    []*RoleAssignment
}

type RoleAssignment struct {
	// Role example:
	// "/subscriptions/<sub-id>/providers/Microsoft.Authorization/roleDefinitions/<role-guid>"
	RoleDefinitionID string
	// Scope example:
	// "/subscriptions/<sub-id>/resourceGroups/<rg>/providers/Microsoft.KeyVault/vaults/<kv-name>"
	Scope string
}

// NewDriver creates a new AKS driver instance that uses the Azure SDK to
// provision and manage an Azure AKS cluster for running tests.
func NewDriver(name string, opts Options) (drivers.Tester, error) {
	k := &driver{
		name:              name,
		stack:             harness.NewStack(),
		resourceGroup:     opts.ResourceGroup,
		location:          opts.Location,
		nodeCount:         opts.NodeCount,
		nodeVMSize:        opts.NodeVMSize,
		nodePoolName:      opts.NodePoolName,
		nodeDiskSize:      opts.NodeDiskSize,
		nodeDiskType:      opts.NodeDiskType,
		subscriptionID:    opts.SubscriptionID,
		kubernetesVersion: opts.KubernetesVersion,
		tags:              opts.Tags,
		dnsPrefix:         opts.DNSPrefix,
	}
	if k.location == "" {
		k.location = locationDefault
	}
	if k.nodeCount <= 0 {
		k.nodeCount = nodeCountDefault
	}
	if k.nodeVMSize == "" {
		k.nodeVMSize = nodeVMSizeDefault
	}
	if opts.Timeout != "" {
		timeout, err := time.ParseDuration(opts.Timeout)
		if err != nil {
			return nil, fmt.Errorf("unable to parse timeout setting: %s %v", opts.Timeout, err)
		}
		k.timeout = timeout
	} else {
		k.timeout = timeoutDefault
	}
	if opts.Registries != nil {
		k.registries = opts.Registries
	}
	if k.subscriptionID == "" {
		if v, ok := os.LookupEnv("AZURE_SUBSCRIPTION_ID"); ok {
			log.Infof("Using subscription from AZURE_SUBSCRIPTION_ID")
			k.subscriptionID = v
		} else {
			return nil, fmt.Errorf("no Azure subscription specified")
		}
	}
	if k.resourceGroup == "" {
		if v, ok := os.LookupEnv("AZURE_RESOURCE_GROUP"); ok {
			log.Infof("Using resource group from AZURE_RESOURCE_GROUP")
			k.resourceGroup = v
		} else {
			return nil, fmt.Errorf("no Azure resource group specified")
		}
	}
	switch k.nodeDiskType {
	case "", "Ephemeral", "Managed":
	default:
		return nil, fmt.Errorf(
			"invalid node disk type: %s, supported types: Ephemeral, Managed", k.nodeDiskType)
	}
	if opts.PodIdentityAssociations != nil {
		for _, v := range opts.PodIdentityAssociations {
			if v == nil {
				continue
			}
			podIdentityAssociation := &PodIdentityAssociationOptions{
				Namespace:          v.Namespace,
				ServiceAccountName: v.ServiceAccountName,
			}
			for _, role := range v.RoleAssignments {
				if role == nil {
					continue
				}
				podIdentityAssociation.RoleAssignments = append(
					podIdentityAssociation.RoleAssignments, role,
				)
			}
			k.podIdentityAssociations = append(k.podIdentityAssociations, podIdentityAssociation)
		}
	}
	return k, nil
}

func (k *driver) Setup(ctx context.Context) error {
	log := clog.FromContext(ctx)

	// Obtain Azure credentials based on environment variables.
	aksCred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return fmt.Errorf("unable to obtain Azure credentials: %v", err)
	}
	k.aksCred = aksCred

	aksClient, err := armcontainerservice.NewManagedClustersClient(
		k.subscriptionID, k.aksCred, nil)
	if err != nil {
		return fmt.Errorf("unable to create AKS client: %v", err)
	}
	k.aksClient = aksClient

	if n, ok := os.LookupEnv("IMAGETEST_AKS_CLUSTER"); ok {
		log.Infof("Using cluster name from IMAGETEST_AKS_CLUSTER: %s", n)
		k.clusterName = n
	} else {
		uid := "imagetest-" + uuid.New().String()
		log.Infof("Using random cluster name: %s", uid)
		k.clusterName = uid
	}
	if k.dnsPrefix == "" {
		log.Infof("No DNS prefix specified, using the cluster name: %s",
			k.clusterName)
		k.dnsPrefix = k.clusterName
	}

	if k.nodePoolName == "" {
		k.nodePoolName = defaultPoolName
	}

	cfg, err := os.Create(filepath.Join(os.TempDir(), k.clusterName))
	if err != nil {
		return fmt.Errorf("failed creating temp dir: %w", err)
	}

	log.Infof("Using kubeconfig: %s", cfg.Name())
	k.kubeconfig = cfg.Name()

	if _, ok := os.LookupEnv("IMAGETEST_AKS_CLUSTER"); ok {
		log.Infof("Using existing AKS cluster.")
	} else {
		err = k.createCluster(ctx)
		if err != nil {
			return err
		}
	}

	err = k.createPodIdentityAssociation(ctx)
	if err != nil {
		return err
	}

	err = k.writeKubeConfig(ctx)
	if err != nil {
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

	var nodeDiskType *armcontainerservice.OSDiskType = nil
	switch k.nodeDiskType {
	case "Ephemeral":
		nodeDiskType = to.Ptr(armcontainerservice.OSDiskTypeEphemeral)
	case "Managed":
		nodeDiskType = to.Ptr(armcontainerservice.OSDiskTypeManaged)
	}

	workload_identity_enabled := k.podIdentityAssociations != nil
	tags := k.buildTags()

	// Azure creates a secondary resource group for the cluster nodes.
	// Unless we specify a name, it will be derived based on the cluster name,
	// resource group and region name, which is likely to exceed the maximum
	// length of 80 characters.
	clusterNodeRG := fmt.Sprintf("%s-%s", k.resourceGroup, k.clusterName)

	log.Infof("Creating AKS cluster: %v. "+
		"Resource group: %v, node resource group: %v, pool name: %v, node count: %v, "+
		"node vm size: %v, node disk size: %v, "+
		"node disk type: %v, tags: %v, workload identity enabled: %v, dns prefix: %v",
		k.clusterName,
		k.resourceGroup,
		clusterNodeRG,
		k.nodePoolName,
		k.nodeCount,
		k.nodeVMSize,
		k.nodeDiskSize,
		nodeDiskType,
		tags,
		workload_identity_enabled,
		k.dnsPrefix)

	poller, err := k.aksClient.BeginCreateOrUpdate(
		ctx,
		k.resourceGroup,
		k.clusterName,
		armcontainerservice.ManagedCluster{
			Location: &k.location,
			Identity: &armcontainerservice.ManagedClusterIdentity{
				Type: to.Ptr(armcontainerservice.ResourceIdentityTypeSystemAssigned),
			},
			Tags: tags,
			Properties: &armcontainerservice.ManagedClusterProperties{
				DNSPrefix:         &k.dnsPrefix,
				NodeResourceGroup: &clusterNodeRG,
				AgentPoolProfiles: []*armcontainerservice.ManagedClusterAgentPoolProfile{
					{
						Name:         &k.nodePoolName,
						Count:        &k.nodeCount,
						VMSize:       &k.nodeVMSize,
						OSDiskSizeGB: &k.nodeDiskSize,
						OSDiskType:   nodeDiskType,
						Mode:         to.Ptr(armcontainerservice.AgentPoolModeSystem),
						OSType:       to.Ptr(armcontainerservice.OSTypeLinux),
						Type:         to.Ptr(armcontainerservice.AgentPoolTypeVirtualMachineScaleSets),
					},
				},
				NetworkProfile: &armcontainerservice.NetworkProfile{
					NetworkPlugin: to.Ptr(armcontainerservice.NetworkPluginAzure),
				},
				OidcIssuerProfile: &armcontainerservice.ManagedClusterOIDCIssuerProfile{
					Enabled: &workload_identity_enabled,
				},
				SecurityProfile: &armcontainerservice.ManagedClusterSecurityProfile{
					WorkloadIdentity: &armcontainerservice.ManagedClusterSecurityProfileWorkloadIdentity{
						Enabled: &workload_identity_enabled,
					},
				},
			},
		},
		nil,
	)
	if err != nil {
		return fmt.Errorf("failed to iniate AKS cluster creation: %v", err)
	}

	if err := k.stack.Add(func(ctx context.Context) error {
		return k.teardownCluster(ctx)
	}); err != nil {
		return err
	}

	log.Infof("Waiting for AKS cluster provisioning.")
	resp, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
		Frequency: pollFrequency,
	})
	if err != nil {
		return fmt.Errorf("failed to create AKS cluster: %v", err)
	}

	log.Infof("Created AKS cluster: %s", resp.ID)
	return nil
}

// Please refer to the official AKS documentation:
//
//	https://learn.microsoft.com/en-us/azure/aks/workload-identity-overview
//	https://learn.microsoft.com/en-us/graph/api/resources/federatedidentitycredentials-overview
func (k *driver) createPodIdentityAssociation(ctx context.Context) error {
	log := clog.FromContext(ctx)

	if k.podIdentityAssociations == nil {
		log.Infof("No pod identity associations provided.")
		return nil
	}

	log.Infof("Preparing pod identity assoiations.")

	aksUAIClient, err := armmsi.NewUserAssignedIdentitiesClient(
		k.subscriptionID, k.aksCred, nil)
	if err != nil {
		return fmt.Errorf("unable to create user assigned idenitity client: %v", err)
	}
	aksFICClient, err := armmsi.NewFederatedIdentityCredentialsClient(
		k.subscriptionID, k.aksCred, nil)
	if err != nil {
		return fmt.Errorf("unable to create federated idenitity client: %v", err)
	}
	aksRoleClient, err := armauthorization.NewRoleAssignmentsClient(
		k.subscriptionID, k.aksCred, nil)
	if err != nil {
		return fmt.Errorf("unable to create role client: %v", err)
	}

	cluster, err := k.aksClient.Get(ctx, k.resourceGroup, k.clusterName, nil)
	if err != nil {
		return fmt.Errorf("unable to retrieve cluster: %v", err)
	}
	if cluster.Properties.OidcIssuerProfile.IssuerURL == nil {
		err = k.enableWorkloadIdenity(ctx)
		if err != nil {
			return err
		}

		// Refresh the cluster info.
		cluster, err := k.aksClient.Get(ctx, k.resourceGroup, k.clusterName, nil)
		if err != nil {
			return fmt.Errorf("unable to retrieve cluster: %v", err)
		}

		if cluster.Properties.OidcIssuerProfile.IssuerURL == nil {
			return fmt.Errorf("OIDC issuer URL missing after enabling Workload Identity")
		}
	}
	oidcIssuerURL := *cluster.Properties.OidcIssuerProfile.IssuerURL

	for _, v := range k.podIdentityAssociations {
		if v == nil {
			continue
		}

		identityName := fmt.Sprintf(
			"%s-%s-%s", k.clusterName, v.Namespace, v.ServiceAccountName)
		federatedIdentityName := fmt.Sprintf("%s-fed", identityName)

		log.Infof("Creating user assigned identity: %s.", identityName)
		miResp, err := aksUAIClient.CreateOrUpdate(
			ctx,
			k.resourceGroup,
			identityName,
			armmsi.Identity{
				Location: &k.location,
			},
			nil,
		)
		if err != nil {
			return fmt.Errorf("unable to create identity: %v", err)
		}

		if err := k.stack.Add(func(ctx context.Context) error {
			_, err = aksUAIClient.Delete(
				ctx,
				k.resourceGroup,
				identityName,
				nil,
			)
			if err != nil {
				return fmt.Errorf("unable to delete identity: %v", err)
			}
			return nil
		}); err != nil {
			return err
		}

		principalID := *miResp.Properties.PrincipalID
		credentialSubject := fmt.Sprintf("system:serviceaccount:%s:%s",
			v.Namespace, v.ServiceAccountName,
		)

		log.Infof("Creating federated identity: %s, subject: %s.",
			federatedIdentityName, credentialSubject)
		_, err = aksFICClient.CreateOrUpdate(
			ctx,
			k.resourceGroup,
			identityName,
			federatedIdentityName,
			armmsi.FederatedIdentityCredential{
				Properties: &armmsi.FederatedIdentityCredentialProperties{
					Issuer:  &oidcIssuerURL,
					Subject: &credentialSubject,
					Audiences: []*string{
						to.Ptr("api://AzureADTokenExchange"),
					},
				},
			},
			nil,
		)
		if err != nil {
			return fmt.Errorf("unable to create federated identity: %v", err)
		}

		if err := k.stack.Add(func(ctx context.Context) error {
			_, err = aksFICClient.Delete(
				ctx,
				k.resourceGroup,
				identityName,
				federatedIdentityName,
				nil,
			)
			if err != nil {
				return fmt.Errorf("unable to delete federated identity: %v", err)
			}
			return nil
		}); err != nil {
			return err
		}

		assignmentName := uuid.New().String()

		for _, role := range v.RoleAssignments {
			log.Infof("Creating role assignment: %s. "+
				"Principal ID: %s, role: %s, scope: %s.",
				assignmentName, principalID, role.RoleDefinitionID, role.Scope)
			_, err = aksRoleClient.Create(
				ctx,
				role.Scope,
				assignmentName,
				armauthorization.RoleAssignmentCreateParameters{
					Properties: &armauthorization.RoleAssignmentProperties{
						RoleDefinitionID: to.Ptr(role.RoleDefinitionID),
						PrincipalID:      &principalID,
					},
				},
				nil,
			)
			if err != nil {
				return fmt.Errorf("unable to create role assignment: %v", err)
			}

			if err := k.stack.Add(func(ctx context.Context) error {
				_, err := aksRoleClient.Delete(
					ctx,
					role.Scope,
					assignmentName,
					nil,
				)
				if err != nil {
					return fmt.Errorf("unable to delete role assignment: %v", err)
				}
				return nil
			}); err != nil {
				return err
			}
		}

		log.Infof("Created pod identity association for service account %s/%s for cluster %s.",
			v.Namespace, v.ServiceAccountName, k.clusterName)
	}

	return nil
}

// Enable AKS Workload Identity (OIDC provider) on an existing cluster.
func (k *driver) enableWorkloadIdenity(ctx context.Context) error {
	log.Info("Enabling AKS Workload Identity.")
	resp, err := k.aksClient.Get(ctx, k.resourceGroup, k.clusterName, nil)
	if err != nil {
		return fmt.Errorf("failed to retrieve cluster: %v", err)
	}
	cluster := resp.ManagedCluster
	if cluster.Properties == nil {
		cluster.Properties = &armcontainerservice.ManagedClusterProperties{}
	}
	cluster.Properties.OidcIssuerProfile = &armcontainerservice.ManagedClusterOIDCIssuerProfile{
		Enabled: to.Ptr(true),
	}
	cluster.Properties.SecurityProfile = &armcontainerservice.ManagedClusterSecurityProfile{
		WorkloadIdentity: &armcontainerservice.ManagedClusterSecurityProfileWorkloadIdentity{
			Enabled: to.Ptr(true),
		},
	}

	poller, err := k.aksClient.BeginCreateOrUpdate(
		ctx, k.resourceGroup, k.clusterName, cluster, nil)
	if err != nil {
		return fmt.Errorf("failed to initate Workload Identity update: %v", err)
	}

	log.Info("Waiting for cluster update.")
	_, err = poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
		Frequency: pollFrequency,
	})
	if err != nil {
		return fmt.Errorf("failed to enable AKS Workload Identity: %v", err)
	}

	log.Info("Enabled AKS Workload Identity.")
	return nil
}

func (k *driver) writeKubeConfig(ctx context.Context) error {
	log := clog.FromContext(ctx)
	log.Infof("Preparing kubeconfig: %s", k.kubeconfig)

	creds, err := k.aksClient.ListClusterAdminCredentials(
		ctx, k.resourceGroup, k.clusterName, nil)
	if err != nil {
		return fmt.Errorf("failed to retrieve kubeconfig: %v", err)
	}

	if len(creds.Kubeconfigs) == 0 {
		return fmt.Errorf("no kubeconfigs retrieved")
	}

	err = os.WriteFile(k.kubeconfig, creds.Kubeconfigs[0].Value, 0o644)
	if err != nil {
		return fmt.Errorf("unable to write kubeconfig: %s %v", k.kubeconfig, err)
	}

	return nil
}

func (k *driver) buildTags() map[string]*string {
	tags := map[string]*string{
		"imagetest":              to.Ptr("true"),
		"imagetest:test-name":    &k.name,
		"imagetest:cluster-name": &k.clusterName,
	}
	for k, v := range k.tags {
		tags[k] = &v
	}
	return tags
}

func (k *driver) teardownCluster(ctx context.Context) error {
	log := clog.FromContext(ctx)
	if v := os.Getenv("IMAGETEST_AKS_SKIP_TEARDOWN"); v == "true" {
		log.Info("Skipping AKS teardown due to IMAGETEST_AKS_SKIP_TEARDOWN=true")
		return nil
	}
	if _, ok := os.LookupEnv("IMAGETEST_AKS_CLUSTER"); ok {
		log.Infof("Skipping AKS teardown due to existing cluster: IMAGETEST_AKS_CLUSTER.")
		return nil
	}

	log.Info("Initating cluster teardown.")
	poller, err := k.aksClient.BeginDelete(ctx, k.resourceGroup, k.clusterName, nil)
	if err != nil {
		return fmt.Errorf("failed to initate AKS cluster teardown: %v", err)
	}

	log.Info("Waiting for cluster teardown.")
	_, err = poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{
		Frequency: pollFrequency,
	})
	if err != nil {
		return fmt.Errorf("failed to delete AKS cluster: %v", err)
	}

	log.Info("Cluster teardown succeeded.")
	return nil
}

func (k *driver) Teardown(ctx context.Context) error {
	log := clog.FromContext(ctx)
	log.Info("Initating resource teardown.")

	// Avoid reusing the original context, it may have already timed out.
	// TODO: consider moving this to the caller.
	teardownCtx, cancel := context.WithTimeout(context.Background(), k.timeout)
	defer cancel()

	return k.stack.Teardown(teardownCtx)
}

func (k *driver) Run(ctx context.Context, ref name.Reference) (*drivers.RunResult, error) {
	// Build docker config from registries for pod authentication
	log := clog.FromContext(ctx)
	log.Info("Running %v.", ref)
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
			"IMAGETEST_DRIVER": "aks",
		}),
		pod.WithRegistryStaticAuth(dcfg),
	)
}
