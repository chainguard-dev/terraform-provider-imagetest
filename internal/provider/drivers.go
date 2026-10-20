package provider

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
	aks "github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers/aks"
	dockerindocker "github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers/docker_in_docker"
	mc2 "github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers/ec2"
	ekswitheksctl "github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers/eks_with_eksctl"
	k3sindocker "github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers/k3s_in_docker"
	sshdriver "github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers/ssh"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type DriverResourceModel string

const (
	DriverAKS            DriverResourceModel = "aks"
	DriverK3sInDocker    DriverResourceModel = "k3s_in_docker"
	DriverDockerInDocker DriverResourceModel = "docker_in_docker"
	DriverEKSWithEksctl  DriverResourceModel = "eks_with_eksctl"
	DriverEC2            DriverResourceModel = "ec2"
	DriverSSH            DriverResourceModel = "ssh"
)

type TestsDriversResourceModel struct {
	AKS            *AKSDriverResourceModel            `tfsdk:"aks"`
	K3sInDocker    *K3sInDockerDriverResourceModel    `tfsdk:"k3s_in_docker"`
	DockerInDocker *DockerInDockerDriverResourceModel `tfsdk:"docker_in_docker"`
	EKSWithEksctl  *EKSWithEksctlDriverResourceModel  `tfsdk:"eks_with_eksctl"`
	EC2            *EC2DriverResourceModel            `tfsdk:"ec2"`
	SSH            *SSHDriverResourceModel            `tfsdk:"ssh"`
}

type AKSDriverResourceModel struct {
	ResourceGroup               types.String                                  `tfsdk:"resource_group"`
	NodeResourceGroup           types.String                                  `tfsdk:"node_resource_group"`
	Location                    types.String                                  `tfsdk:"location"`
	DNSPrefix                   types.String                                  `tfsdk:"dns_prefix"`
	NodeCount                   types.Int32                                   `tfsdk:"node_count"`
	NodeVMSize                  types.String                                  `tfsdk:"node_vm_size"`
	NodeDiskSize                types.Int32                                   `tfsdk:"node_disk_size"`
	NodeDiskType                types.String                                  `tfsdk:"node_disk_type"`
	NodePoolName                types.String                                  `tfsdk:"node_pool_name"`
	SubscriptionID              types.String                                  `tfsdk:"subscription_id"`
	KubernetesVersion           types.String                                  `tfsdk:"kubernetes_version"`
	Tags                        map[string]string                             `tfsdk:"tags"`
	Timeouts                    *DriverTimeoutsResourceModel                  `tfsdk:"timeouts"`
	PodIdentityAssociations     []*AKSPodIdentityAssociationResourceModel     `tfsdk:"pod_identity_associations"`
	ClusterIdentityAssociations []*AKSClusterIdentityAssociationResourceModel `tfsdk:"cluster_identity_associations"`
	AttachedACRs                []*AKSAttachedACR                             `tfsdk:"attached_acrs"`
}

type AKSPodIdentityAssociationResourceModel struct {
	ServiceAccountName types.String         `tfsdk:"service_account_name"`
	Namespace          types.String         `tfsdk:"namespace"`
	RoleAssignments    []*AKSRoleAssignment `tfsdk:"role_assignments"`
}

type AKSClusterIdentityAssociationResourceModel struct {
	IdentityName    types.String         `tfsdk:"identity_name"`
	RoleAssignments []*AKSRoleAssignment `tfsdk:"role_assignments"`
}

type AKSRoleAssignment struct {
	RoleDefinitionID types.String `tfsdk:"role_definition_id"`
	Scope            types.String `tfsdk:"scope"`
}

type AKSAttachedACR struct {
	ResourceGroup   types.String `tfsdk:"resource_group"`
	Name            types.String `tfsdk:"name"`
	CreateIfMissing types.Bool   `tfsdk:"create_if_missing"`
}

type K3sInDockerDriverResourceModel struct {
	Image         types.String                                         `tfsdk:"image"`
	Cni           types.Bool                                           `tfsdk:"cni"`
	NetworkPolicy types.Bool                                           `tfsdk:"network_policy"`
	Traefik       types.Bool                                           `tfsdk:"traefik"`
	MetricsServer types.Bool                                           `tfsdk:"metrics_server"`
	Registries    map[string]*K3sInDockerDriverRegistriesResourceModel `tfsdk:"registries"`
	Snapshotter   types.String                                         `tfsdk:"snapshotter"`
	Hooks         *K3sInDockerDriverHooksModel                         `tfsdk:"hooks"`
	Timeouts      *DriverTimeoutsResourceModel                         `tfsdk:"timeouts"`
}

type K3sInDockerDriverRegistriesResourceModel struct {
	Mirrors *K3sInDockerDriverRegistriesMirrorResourceModel `tfsdk:"mirrors"`
}

type K3sInDockerDriverRegistriesMirrorResourceModel struct {
	Endpoints []string `tfsdk:"endpoints"`
}

type K3sInDockerDriverHooksModel struct {
	PostStart []string `tfsdk:"post_start"`
}

type DockerInDockerDriverResourceModel struct {
	Image    types.String                 `tfsdk:"image"`
	Mirrors  []string                     `tfsdk:"mirrors"`
	Timeouts *DriverTimeoutsResourceModel `tfsdk:"timeouts"`
}

type EKSWithEksctlDriverResourceModel struct {
	Region                  types.String                                         `tfsdk:"region"`
	KubernetesVersion       types.String                                         `tfsdk:"kubernetes_version"`
	NodeAMI                 types.String                                         `tfsdk:"node_ami"`
	NodeType                types.String                                         `tfsdk:"node_type"`
	NodeCount               types.Int64                                          `tfsdk:"node_count"`
	Timeouts                *DriverTimeoutsResourceModel                         `tfsdk:"timeouts"`
	Storage                 *EKSWithEksctlStorageResourceModel                   `tfsdk:"storage"`
	PodIdentityAssociations []*EKSWithEksctlPodIdentityAssociationResourceModule `tfsdk:"pod_identity_associations"`
	AWSProfile              types.String                                         `tfsdk:"aws_profile"`
	Tags                    map[string]string                                    `tfsdk:"tags"`
}

// DriverTimeoutsResourceModel is the shared schema model for driver
// lifecycle timeouts. All drivers that support the timeouts block
// should embed this type.
type DriverTimeoutsResourceModel struct {
	Setup    types.String `tfsdk:"setup"`
	Teardown types.String `tfsdk:"teardown"`
}

func driverTimeoutsSchema() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Description: "Timeout configuration for driver lifecycle phases.",
		Optional:    true,
		Attributes: map[string]schema.Attribute{
			"setup": schema.StringAttribute{
				Description: "Maximum time for driver setup (e.g., cluster creation). If unset, setup is bounded only by the resource-level timeout.",
				Optional:    true,
			},
			"teardown": schema.StringAttribute{
				Description: "Maximum time for driver teardown (e.g., cluster deletion). If unset, the driver uses a built-in default.",
				Optional:    true,
			},
		},
	}
}

type EKSWithEksctlStorageResourceModel struct {
	Size types.String `tfsdk:"size"`
	Type types.String `tfsdk:"type"`
}

type EKSWithEksctlPodIdentityAssociationResourceModule struct {
	PermissionPolicyARN types.String `tfsdk:"permission_policy_arn"`
	ServiceAccountName  types.String `tfsdk:"service_account_name"`
	Namespace           types.String `tfsdk:"namespace"`
}

type EC2DriverResourceModel struct {
	VPCID               types.String                      `tfsdk:"vpc_id"`
	Region              types.String                      `tfsdk:"region"`
	AMI                 types.String                      `tfsdk:"ami"`
	InstanceType        types.String                      `tfsdk:"instance_type"`
	RootVolumeSize      types.Int64                       `tfsdk:"root_volume_size"`
	InstanceProfileName types.String                      `tfsdk:"instance_profile_name"`
	SubnetCIDR          types.String                      `tfsdk:"subnet_cidr"`
	SSHUser             types.String                      `tfsdk:"ssh_user"`
	SSHPort             types.Int64                       `tfsdk:"ssh_port"`
	Shell               types.String                      `tfsdk:"shell"`
	SetupCommands       []types.String                    `tfsdk:"setup_commands"`
	Env                 types.Map                         `tfsdk:"env"`
	UserData            types.String                      `tfsdk:"user_data"`
	VolumeMounts        []types.String                    `tfsdk:"volume_mounts"`
	DeviceMounts        []types.String                    `tfsdk:"device_mounts"`
	GPUs                types.String                      `tfsdk:"gpus"`
	MountAllGPUs        types.Bool                        `tfsdk:"mount_all_gpus"` // Deprecated: use gpus = "all" instead
	ExistingInstance    *EC2ExistingInstanceResourceModel `tfsdk:"existing_instance"`
	OnFailure           []string                          `tfsdk:"on_failure"`
	Timeouts            *DriverTimeoutsResourceModel      `tfsdk:"timeouts"`
}

type EC2ExistingInstanceResourceModel struct {
	IP         types.String `tfsdk:"ip"`
	SSHKey     types.String `tfsdk:"ssh_key"`
	KnownHosts types.String `tfsdk:"known_hosts"`
}

type SSHDriverResourceModel struct {
	LockPath      types.String                 `tfsdk:"lock_path"`
	LockTimeout   types.String                 `tfsdk:"lock_timeout"`
	Host          types.String                 `tfsdk:"host"`
	SSHPort       types.Int64                  `tfsdk:"ssh_port"`
	SSHUser       types.String                 `tfsdk:"ssh_user"`
	SSHKey        types.String                 `tfsdk:"ssh_key"`
	KnownHosts    types.String                 `tfsdk:"known_hosts"`
	Shell         types.String                 `tfsdk:"shell"`
	SetupCommands []types.String               `tfsdk:"setup_commands"`
	Env           types.Map                    `tfsdk:"env"`
	VolumeMounts  []types.String               `tfsdk:"volume_mounts"`
	DeviceMounts  []types.String               `tfsdk:"device_mounts"`
	GPUs          types.String                 `tfsdk:"gpus"`
	OnFailure     []string                     `tfsdk:"on_failure"`
	Timeouts      *DriverTimeoutsResourceModel `tfsdk:"timeouts"`
}

func parseTimeoutsModel(m *DriverTimeoutsResourceModel) (drivers.Timeouts, error) {
	if m == nil {
		return drivers.Timeouts{}, nil
	}
	return drivers.ParseTimeouts(m.Setup.ValueString(), m.Teardown.ValueString())
}

// LoadDriver creates and configures a driver instance based on the specified driver type.
func (t TestsResource) LoadDriver(ctx context.Context, data *TestsResourceModel) (drivers.Tester, error) {
	driversCfg := data.Drivers
	if driversCfg == nil {
		driversCfg = &TestsDriversResourceModel{}
	}

	id := data.Id.ValueString()
	timeout := data.Timeout.ValueString()

	repo := t.repo
	if data.RepoOverride.ValueString() != "" {
		var err error
		repo, err = name.NewRepository(data.RepoOverride.ValueString())
		if err != nil {
			return nil, fmt.Errorf("failed to parse repo override: %w", err)
		}
	}

	switch data.Driver {
	case DriverAKS:
		cfg := driversCfg.AKS
		if cfg == nil {
			cfg = &AKSDriverResourceModel{}
		}

		// Build registry auth config from the resolved repo.
		// TODO: consider reusing the registry related code since it's not driver
		// specific.
		registries := make(map[string]*aks.RegistryConfig)
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
		registries[repo.RegistryStr()] = &aks.RegistryConfig{
			Auth: &aks.RegistryAuthConfig{
				Username: acfg.Username,
				Password: acfg.Password,
				Auth:     acfg.Auth,
			},
		}
		podIdentityAssociations := []*aks.PodIdentityAssociationOptions{}
		if cfg.PodIdentityAssociations != nil {
			for _, v := range cfg.PodIdentityAssociations {
				association := new(aks.PodIdentityAssociationOptions)
				association.ServiceAccountName = v.ServiceAccountName.ValueString()
				association.Namespace = v.Namespace.ValueString()
				roleAssignments := []*aks.RoleAssignment{}
				for _, in_assignment := range v.RoleAssignments {
					out_assignment := new(aks.RoleAssignment)
					out_assignment.RoleDefinitionID = in_assignment.RoleDefinitionID.ValueString()
					out_assignment.Scope = in_assignment.Scope.ValueString()
					roleAssignments = append(roleAssignments, out_assignment)
				}
				association.RoleAssignments = roleAssignments

				podIdentityAssociations = append(podIdentityAssociations, association)
			}
		}
		clusterIdentityAssociations := []*aks.ClusterIdentityAssociationOptions{}
		if cfg.ClusterIdentityAssociations != nil {
			for _, v := range cfg.ClusterIdentityAssociations {
				association := new(aks.ClusterIdentityAssociationOptions)
				association.IdentityName = v.IdentityName.ValueString()
				roleAssignments := []*aks.RoleAssignment{}
				for _, in_assignment := range v.RoleAssignments {
					out_assignment := new(aks.RoleAssignment)
					out_assignment.RoleDefinitionID = in_assignment.RoleDefinitionID.ValueString()
					out_assignment.Scope = in_assignment.Scope.ValueString()
					roleAssignments = append(roleAssignments, out_assignment)
				}
				association.RoleAssignments = roleAssignments

				clusterIdentityAssociations = append(clusterIdentityAssociations, association)
			}
		}
		attachedACRs := []*aks.AttachedACR{}
		if cfg.AttachedACRs != nil {
			for _, v := range cfg.AttachedACRs {
				acr := aks.AttachedACR{
					Name:            v.Name.ValueString(),
					ResourceGroup:   v.ResourceGroup.ValueString(),
					CreateIfMissing: v.CreateIfMissing.ValueBool(),
				}

				attachedACRs = append(attachedACRs, &acr)
			}
		}

		return aks.NewDriver(id, aks.Options{
			ResourceGroup:               cfg.ResourceGroup.ValueString(),
			NodeResourceGroup:           cfg.NodeResourceGroup.ValueString(),
			Location:                    cfg.Location.ValueString(),
			DNSPrefix:                   cfg.DNSPrefix.ValueString(),
			NodeCount:                   cfg.NodeCount.ValueInt32(),
			NodeVMSize:                  cfg.NodeVMSize.ValueString(),
			NodeDiskSize:                cfg.NodeDiskSize.ValueInt32(),
			NodeDiskType:                cfg.NodeDiskType.ValueString(),
			NodePoolName:                cfg.NodePoolName.ValueString(),
			Timeout:                     timeout,
			SubscriptionID:              cfg.SubscriptionID.ValueString(),
			KubernetesVersion:           cfg.KubernetesVersion.ValueString(),
			Tags:                        cfg.Tags,
			Registries:                  registries,
			PodIdentityAssociations:     podIdentityAssociations,
			ClusterIdentityAssociations: clusterIdentityAssociations,
			AttachedACRs:                attachedACRs,
		})

	case DriverK3sInDocker:
		cfg := driversCfg.K3sInDocker
		if cfg == nil {
			cfg = &K3sInDockerDriverResourceModel{}
		}

		opts := []k3sindocker.DriverOpts{
			k3sindocker.WithRegistry(repo.RegistryStr()),
		}

		for _, extraRepo := range t.extraRepos {
			opts = append(opts, k3sindocker.WithRegistry(extraRepo.RegistryStr()))
		}

		if cfg.Image.ValueString() != "" {
			opts = append(opts, k3sindocker.WithImageRef(cfg.Image.ValueString()))
		}

		if cfg.Cni.ValueBool() {
			opts = append(opts, k3sindocker.WithCNI(true))
		}

		if cfg.NetworkPolicy.ValueBool() {
			opts = append(opts, k3sindocker.WithNetworkPolicy(true))
		}

		if cfg.Traefik.ValueBool() {
			opts = append(opts, k3sindocker.WithTraefik(true))
		}

		if cfg.MetricsServer.ValueBool() {
			opts = append(opts, k3sindocker.WithMetricsServer(true))
		}

		if cfg.Snapshotter.ValueString() != "" {
			opts = append(opts, k3sindocker.WithSnapshotter(cfg.Snapshotter.ValueString()))
		}

		// "native" snapshotter is required for environments already running docker in docker
		if os.Getenv("WORKSTATION") != "" {
			opts = append(opts, k3sindocker.WithSnapshotter("native"))
		}

		if registries := cfg.Registries; registries != nil {
			for k, v := range registries {
				if v.Mirrors != nil {
					for _, mirror := range v.Mirrors.Endpoints {
						opts = append(opts, k3sindocker.WithRegistryMirror(k, mirror))
					}
				}
			}
		}

		if hooks := cfg.Hooks; hooks != nil {
			for _, hook := range hooks.PostStart {
				opts = append(opts, k3sindocker.WithPostStartHook(hook))
			}
		}

		// If the user specified registry is "localhost:#", set a mirror to "host.docker.internal:#"
		if isLocalRegistry(t.repo.Registry) {
			parts := strings.Split(t.repo.RegistryStr(), ":")
			if len(parts) != 2 {
				return nil, fmt.Errorf("invalid registry: %s", t.repo.RegistryStr())
			}
			// Configure containerd to use host.docker.internal as the default registry mirror
			opts = append(opts, k3sindocker.WithRegistryMirror(t.repo.RegistryStr(), fmt.Sprintf("http://host.docker.internal:%s", parts[1])))

			// Configure the test pods to resolve host.docker.internal to the host's gateway IP
			coreDNSHook := `
HOST_IP=$(grep "host.docker.internal" /etc/hosts | awk '{print $1}' | head -1)
if [ -z "$HOST_IP" ]; then
  echo "Failed to resolve host.docker.internal"
  exit 1
fi

cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: ConfigMap
metadata:
  name: coredns-custom
  namespace: kube-system
data:
  hostdocker.server: |
    host.docker.internal:53 {
      hosts {
        $HOST_IP host.docker.internal
        fallthrough
      }
    }
EOF

# CoreDNS is applied asynchronously by the k3s addon manager, so it may not
# exist yet when this hook runs. Wait for it before triggering a rollout to
# load the configmap above. Restarting (rather than relying on CoreDNS's
# reload plugin) is intentional: it gives a deterministic point at which the
# new config is live before tests start.
kubectl wait --for=create deployment/coredns -n kube-system --timeout=3m
kubectl rollout restart deployment/coredns -n kube-system
kubectl rollout status deployment/coredns -n kube-system --timeout=60s
`
			opts = append(opts, k3sindocker.WithPostStartHook(coreDNSHook))
		}

		timeouts, err := parseTimeoutsModel(cfg.Timeouts)
		if err != nil {
			return nil, fmt.Errorf("k3s_in_docker: %w", err)
		}
		opts = append(opts, k3sindocker.WithTimeouts(timeouts))

		// Reserve a unique, private (0600) path for the kubeconfig the
		// driver writes during Setup. From here on the driver owns it and
		// removes it on Teardown; only a failure to build the driver leaves
		// it to us.
		tf, err := os.CreateTemp("", "imagetest-k3s-in-docker")
		if err != nil {
			return nil, err
		}
		if err := tf.Close(); err != nil {
			return nil, errors.Join(err, os.Remove(tf.Name()))
		}
		opts = append(opts, k3sindocker.WithWriteKubeconfig(tf.Name()))

		dr, err := k3sindocker.NewDriver(id, opts...)
		if err != nil {
			return nil, errors.Join(err, os.Remove(tf.Name()))
		}
		return dr, nil

	case DriverDockerInDocker:
		cfg := driversCfg.DockerInDocker
		if cfg == nil {
			cfg = &DockerInDockerDriverResourceModel{}
		}

		opts := []dockerindocker.DriverOpts{
			dockerindocker.WithRemoteOptions(t.ropts...),
			dockerindocker.WithRegistryAuth(repo.RegistryStr()),
		}

		for _, extraRepo := range t.extraRepos {
			opts = append(opts, dockerindocker.WithRegistryAuth(extraRepo.RegistryStr()))
		}

		if cfg.Image.ValueString() != "" {
			opts = append(opts, dockerindocker.WithImageRef(cfg.Image.ValueString()))
		}

		if len(cfg.Mirrors) > 0 {
			opts = append(opts, dockerindocker.WithRegistryMirrors(cfg.Mirrors...))
		}

		if isLocalRegistry(repo.Registry) {
			u, err := url.Parse("http://" + repo.RegistryStr())
			if err != nil {
				return nil, fmt.Errorf("failed to parse registry url: %w", err)
			}

			opts = append(opts,
				dockerindocker.WithExtraHosts(
					fmt.Sprintf("%s:%s", u.Hostname(), "127.0.0.1"),
				),
			)
		}

		timeouts, err := parseTimeoutsModel(cfg.Timeouts)
		if err != nil {
			return nil, fmt.Errorf("docker_in_docker: %w", err)
		}
		opts = append(opts, dockerindocker.WithTimeouts(timeouts))

		return dockerindocker.NewDriver(id, opts...)

	case DriverEKSWithEksctl:
		cfg := driversCfg.EKSWithEksctl
		if cfg == nil {
			cfg = &EKSWithEksctlDriverResourceModel{}
		}

		var storageOpts *ekswitheksctl.StorageOptions
		if cfg.Storage != nil {
			storageOpts = &ekswitheksctl.StorageOptions{
				Size: cfg.Storage.Size.ValueString(),
				Type: cfg.Storage.Type.ValueString(),
			}
		}

		podIdentityAssociations := []*ekswitheksctl.PodIdentityAssociationOptions{}
		if cfg.PodIdentityAssociations != nil {
			for _, v := range cfg.PodIdentityAssociations {
				association := new(ekswitheksctl.PodIdentityAssociationOptions)
				association.ServiceAccountName = v.ServiceAccountName.ValueString()
				association.Namespace = v.Namespace.ValueString()
				association.PermissionPolicyARN = v.PermissionPolicyARN.ValueString()

				podIdentityAssociations = append(podIdentityAssociations, association)
			}
		}

		// Build registry auth config from the resolved repo
		registries := make(map[string]*ekswitheksctl.RegistryConfig)
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
		registries[repo.RegistryStr()] = &ekswitheksctl.RegistryConfig{
			Auth: &ekswitheksctl.RegistryAuthConfig{
				Username: acfg.Username,
				Password: acfg.Password,
				Auth:     acfg.Auth,
			},
		}

		timeouts, err := parseTimeoutsModel(cfg.Timeouts)
		if err != nil {
			return nil, fmt.Errorf("eks_with_eksctl: %w", err)
		}

		return ekswitheksctl.NewDriver(id, ekswitheksctl.Options{
			Region:                  cfg.Region.ValueString(),
			KubernetesVersion:       cfg.KubernetesVersion.ValueString(),
			NodeAMI:                 cfg.NodeAMI.ValueString(),
			NodeType:                cfg.NodeType.ValueString(),
			NodeCount:               int(cfg.NodeCount.ValueInt64()),
			PodIdentityAssociations: podIdentityAssociations,
			Storage:                 storageOpts,
			AWSProfile:              cfg.AWSProfile.ValueString(),
			Tags:                    cfg.Tags,
			Timeouts:                timeouts,
			Registries:              registries,
		})

	case DriverEC2:
		log := clog.FromContext(ctx)

		if driversCfg.EC2 == nil {
			return nil, fmt.Errorf("the EC2 driver was specified, but no configuration was provided")
		}

		// Build the driver config
		driverCfg := mc2.Config{
			VPCID:               driversCfg.EC2.VPCID.ValueString(),
			Region:              driversCfg.EC2.Region.ValueString(),
			AMI:                 driversCfg.EC2.AMI.ValueString(),
			InstanceType:        driversCfg.EC2.InstanceType.ValueString(),
			RootVolumeSize:      int32(driversCfg.EC2.RootVolumeSize.ValueInt64()),
			InstanceProfileName: driversCfg.EC2.InstanceProfileName.ValueString(),
			SubnetCIDR:          driversCfg.EC2.SubnetCIDR.ValueString(),
			SSHUser:             driversCfg.EC2.SSHUser.ValueString(),
			Shell:               driversCfg.EC2.Shell.ValueString(),
			UserData:            driversCfg.EC2.UserData.ValueString(),
			GPUs:                driversCfg.EC2.GPUs.ValueString(),
		}

		var err error
		if driverCfg.SSHPort, err = portFromModel("ec2.ssh_port", driversCfg.EC2.SSHPort); err != nil {
			return nil, err
		}

		// Handle deprecated mount_all_gpus field
		if driversCfg.EC2.MountAllGPUs.ValueBool() && driverCfg.GPUs == "" {
			driverCfg.GPUs = "all"
		}

		// Check for skip teardown env var
		if v, ok := os.LookupEnv("IMAGETEST_SKIP_TEARDOWN"); ok && v != "" {
			driverCfg.SkipTeardown = true
		}

		// Handle existing instance mode
		if driversCfg.EC2.ExistingInstance != nil {
			log.Info("using existing instance mode")
			driverCfg.ExistingInstance = &mc2.ExistingInstance{
				IP:         driversCfg.EC2.ExistingInstance.IP.ValueString(),
				SSHKey:     driversCfg.EC2.ExistingInstance.SSHKey.ValueString(),
				KnownHosts: driversCfg.EC2.ExistingInstance.KnownHosts.ValueString(),
			}

			// The existing instance holds no registry credentials, so the
			// driver pulls the images under test with ours before the
			// tests run.
			imgs, err := data.Images.Resolve()
			if err != nil {
				return nil, fmt.Errorf("resolving images for pre-pull: %w", err)
			}
			driverCfg.PrePullImages = prePullRefs(imgs)
		}

		driverCfg.OnFailure = driversCfg.EC2.OnFailure

		// Capture setup commands
		for _, cmd := range driversCfg.EC2.SetupCommands {
			driverCfg.SetupCommands = append(driverCfg.SetupCommands, cmd.ValueString())
		}

		// Capture environment variables
		driverCfg.Env = make(map[string]string)
		for k, v := range driversCfg.EC2.Env.Elements() {
			if v.IsNull() || v.IsUnknown() {
				continue
			}
			if strVal, ok := v.(types.String); ok {
				driverCfg.Env[k] = strVal.ValueString()
			}
		}

		// Capture volume mounts
		for _, mount := range driversCfg.EC2.VolumeMounts {
			driverCfg.VolumeMounts = append(driverCfg.VolumeMounts, mount.ValueString())
		}

		// Capture device mounts
		for _, mount := range driversCfg.EC2.DeviceMounts {
			driverCfg.DeviceMounts = append(driverCfg.DeviceMounts, mount.ValueString())
		}

		// Base64 encode user data if provided
		if driverCfg.UserData != "" {
			driverCfg.UserData = base64.StdEncoding.EncodeToString([]byte(driverCfg.UserData))
		}

		timeouts, err := parseTimeoutsModel(driversCfg.EC2.Timeouts)
		if err != nil {
			return nil, fmt.Errorf("ec2: %w", err)
		}
		driverCfg.Timeouts = timeouts

		// Init AWS config and clients
		awsCfg, err := config.LoadDefaultConfig(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to load AWS configuration: %w", err)
		}
		ec2Client := ec2.NewFromConfig(awsCfg)
		iamClient := iam.NewFromConfig(awsCfg)

		return mc2.NewDriver(driverCfg, ec2Client, iamClient)

	case DriverSSH:
		cfg := driversCfg.SSH
		if cfg == nil {
			return nil, fmt.Errorf("the ssh driver was specified, but no configuration was provided")
		}

		driverCfg := sshdriver.Config{
			Host:       cfg.Host.ValueString(),
			SSHUser:    cfg.SSHUser.ValueString(),
			SSHKey:     cfg.SSHKey.ValueString(),
			KnownHosts: cfg.KnownHosts.ValueString(),
			Shell:      cfg.Shell.ValueString(),
			GPUs:       cfg.GPUs.ValueString(),
			OnFailure:  cfg.OnFailure,
			LockPath:   cfg.LockPath.ValueString(),
			Env:        make(map[string]string),
		}

		var err error
		if driverCfg.SSHPort, err = portFromModel("ssh.ssh_port", cfg.SSHPort); err != nil {
			return nil, err
		}

		if v, ok := os.LookupEnv("IMAGETEST_SKIP_TEARDOWN"); ok && v != "" {
			driverCfg.SkipTeardown = true
		}

		// The host holds no registry credentials, so the driver pulls the
		// images under test with ours before the tests run.
		imgs, err := data.Images.Resolve()
		if err != nil {
			return nil, fmt.Errorf("resolving images for pre-pull: %w", err)
		}
		driverCfg.PrePullImages = prePullRefs(imgs)

		for _, cmd := range cfg.SetupCommands {
			driverCfg.SetupCommands = append(driverCfg.SetupCommands, cmd.ValueString())
		}
		for k, v := range cfg.Env.Elements() {
			if v.IsNull() || v.IsUnknown() {
				continue
			}
			if strVal, ok := v.(types.String); ok {
				driverCfg.Env[k] = strVal.ValueString()
			}
		}
		for _, mount := range cfg.VolumeMounts {
			driverCfg.VolumeMounts = append(driverCfg.VolumeMounts, mount.ValueString())
		}
		for _, mount := range cfg.DeviceMounts {
			driverCfg.DeviceMounts = append(driverCfg.DeviceMounts, mount.ValueString())
		}

		driverCfg.Timeouts, err = parseTimeoutsModel(cfg.Timeouts)
		if err != nil {
			return nil, fmt.Errorf("ssh: %w", err)
		}
		if v := cfg.LockTimeout.ValueString(); v != "" {
			if driverCfg.LockTimeout, err = time.ParseDuration(v); err != nil {
				return nil, fmt.Errorf("ssh: parsing lock_timeout %q: %w", v, err)
			}
		}

		return sshdriver.NewDriver(driverCfg)

	default:
		return nil, fmt.Errorf("no matching driver: %s", data.Driver)
	}
}

// portFromModel narrows a port attribute to int32. Null keeps 0, the
// driver's default; anything outside the TCP range is rejected here rather
// than wrapped by the conversion.
func portFromModel(attr string, v types.Int64) (int32, error) {
	if v.IsNull() || v.IsUnknown() {
		return 0, nil
	}
	p := v.ValueInt64()
	if p < 1 || p > 65535 {
		return 0, fmt.Errorf("%s: %d is out of range (1-65535)", attr, p)
	}
	return int32(p), nil
}

// prePullRefs returns the resolved image references in a stable order, keyed
// by the images map key, for the driver to pull ahead of the tests.
func prePullRefs(imgs map[string]TestsImagesParsed) []string {
	refs := make([]string, 0, len(imgs))
	for _, k := range slices.Sorted(maps.Keys(imgs)) {
		refs = append(refs, imgs[k].Ref)
	}
	return refs
}

func DriverResourceSchema(ctx context.Context) schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Description: "The resource specific driver configuration. This is merged with the provider scoped drivers configuration.",
		Optional:    true,
		Attributes: map[string]schema.Attribute{
			"aks": schema.SingleNestedAttribute{
				Description: "The AKS driver",
				Optional:    true,
				Attributes: map[string]schema.Attribute{
					"resource_group": schema.StringAttribute{
						Description: "The Azure resource group for the AKS driver",
						Optional:    true,
					},
					"node_resource_group": schema.StringAttribute{
						Description: "The Azure resource group to hold AKS node resources",
						Optional:    true,
					},
					"location": schema.StringAttribute{
						Description: "The Azure region for the AKS driver (default is eastus)",
						Optional:    true,
					},
					"dns_prefix": schema.StringAttribute{
						Description: "The DNS prefix of the AKS cluster (uses the cluster name by default)",
						Optional:    true,
					},
					"node_count": schema.Int32Attribute{
						Description: "The number of nodes to use for the AKS driver (default is 1)",
						Optional:    true,
					},
					"node_vm_size": schema.StringAttribute{
						Description: "The node size to use for the AKS driver (default is Standard_DS2_v2)",
						Optional:    true,
					},
					"node_pool_name": schema.StringAttribute{
						Description: "The node pool name to use for the AKS driver",
						Optional:    true,
					},
					"node_disk_size": schema.Int32Attribute{
						Description: "Use a custom VM disk size (GB) instead of the one defined by the VM size",
						Optional:    true,
					},
					"node_disk_type": schema.StringAttribute{
						Description: "Ephemeral or Managed. Defaults to 'Ephemeral', which provide better performance but aren't persistent.",
						Optional:    true,
					},
					"subscription_id": schema.StringAttribute{
						Description: "The Azure subscription ID for the AKS driver, defaults to AZURE_SUBSCRIPTION_ID env var",
						Optional:    true,
					},
					"kubernetes_version": schema.StringAttribute{
						Description: "The Kubernetes version to deploy, uses the Azure default if unspecified",
						Optional:    true,
					},
					"tags": schema.MapAttribute{
						Description: "Additional tags to apply to all AKS resources created by the driver. Auto-generated tags (imagetest, imagetest:test-name, imagetest:cluster-name) are always included.",
						ElementType: types.StringType,
						Optional:    true,
					},
					"pod_identity_associations": schema.ListNestedAttribute{
						Description: "Pod Identity Associations for the AKS driver",
						Optional:    true,
						NestedObject: schema.NestedAttributeObject{
							Attributes: map[string]schema.Attribute{
								"service_account_name": schema.StringAttribute{
									Description: "Name of the Kubernetes service account",
									Optional:    true,
								},
								"namespace": schema.StringAttribute{
									Description: "Kubernetes namespace of the service account",
									Optional:    true,
								},
								"role_assignments": schema.ListNestedAttribute{
									Description: "AKS roles to assign",
									Optional:    true,
									NestedObject: schema.NestedAttributeObject{
										Attributes: map[string]schema.Attribute{
											"role_definition_id": schema.StringAttribute{
												Description: "The role to assign. Example: /subscriptions/<sub-id>/providers/Microsoft.Authorization/roleDefinitions/<role-guid>",
												Optional:    true,
											},
											"scope": schema.StringAttribute{
												Description: "The role assignment scope. Example: /subscriptions/<sub-id>/resourceGroups/<rg>/providers/Microsoft.KeyVault/vaults/<kv-name>",
												Optional:    true,
											},
										},
									},
								},
							},
						},
					},
					"cluster_identity_associations": schema.ListNestedAttribute{
						Description: "Cluster Identity Associations for the AKS driver",
						Optional:    true,
						NestedObject: schema.NestedAttributeObject{
							Attributes: map[string]schema.Attribute{
								"identity_name": schema.StringAttribute{
									Description: "Name of the cluster identity (e.g. kubeletidentity)",
									Optional:    true,
								},
								"role_assignments": schema.ListNestedAttribute{
									Description: "AKS roles to assign",
									Optional:    true,
									NestedObject: schema.NestedAttributeObject{
										Attributes: map[string]schema.Attribute{
											"role_definition_id": schema.StringAttribute{
												Description: "The role to assign. Example: /subscriptions/<sub-id>/providers/Microsoft.Authorization/roleDefinitions/<role-guid>",
												Optional:    true,
											},
											"scope": schema.StringAttribute{
												Description: "The role assignment scope. Example: /subscriptions/<sub-id>/resourceGroups/<rg>/providers/Microsoft.KeyVault/vaults/<kv-name>",
												Optional:    true,
											},
										},
									},
								},
							},
						},
					},
					"attached_acrs": schema.ListNestedAttribute{
						Description: "Attached ACRs, granting image pull rights",
						Optional:    true,
						NestedObject: schema.NestedAttributeObject{
							Attributes: map[string]schema.Attribute{
								"resource_group": schema.StringAttribute{
									Description: "The ACR resource group, defaults to the AKS resource group",
									Optional:    true,
								},
								"name": schema.StringAttribute{
									Description: "",
									Optional:    true,
								},
								"create_if_missing": schema.BoolAttribute{
									Description: "Whether to create the ACR if missing",
									Optional:    true,
								},
							},
						},
					},
					"timeouts": driverTimeoutsSchema(),
				},
			},
			"k3s_in_docker": schema.SingleNestedAttribute{
				Description: "The k3s_in_docker driver",
				Optional:    true,
				Attributes: map[string]schema.Attribute{
					"image": schema.StringAttribute{
						Description: "The image reference to use for the k3s_in_docker driver",
						Optional:    true,
					},
					"cni": schema.BoolAttribute{
						Description: "Enable the CNI plugin",
						Optional:    true,
					},
					"network_policy": schema.BoolAttribute{
						Description: "Enable the network policy",
						Optional:    true,
					},
					"traefik": schema.BoolAttribute{
						Description: "Enable the traefik ingress controller",
						Optional:    true,
					},
					"metrics_server": schema.BoolAttribute{
						Description: "Enable the metrics server",
						Optional:    true,
					},
					"snapshotter": schema.StringAttribute{
						Description: "The snapshotter to use for the k3s_in_docker driver",
						Optional:    true,
					},
					"registries": schema.MapNestedAttribute{
						Description: "A map of registries containing configuration for optional auth, tls, and mirror configuration.",
						Optional:    true,
						NestedObject: schema.NestedAttributeObject{
							Attributes: map[string]schema.Attribute{
								"mirrors": schema.SingleNestedAttribute{
									Description: "A map of registries containing configuration for optional auth, tls, and mirror configuration.",
									Optional:    true,
									Attributes: map[string]schema.Attribute{
										"endpoints": schema.ListAttribute{
											ElementType: types.StringType,
											Optional:    true,
										},
									},
								},
							},
						},
					},
					"hooks": schema.SingleNestedAttribute{
						Description: "Run commands at various lifecycle events",
						Optional:    true,
						Attributes: map[string]schema.Attribute{
							"post_start": schema.ListAttribute{
								ElementType: types.StringType,
								Optional:    true,
							},
						},
					},
					"timeouts": driverTimeoutsSchema(),
				},
			},
			"docker_in_docker": schema.SingleNestedAttribute{
				Description: "The docker_in_docker driver",
				Optional:    true,
				Attributes: map[string]schema.Attribute{
					"image": schema.StringAttribute{
						Description: "The image reference to use for the docker-in-docker driver",
						Optional:    true,
					},
					"mirrors": schema.ListAttribute{
						ElementType: types.StringType,
						Optional:    true,
					},
					"timeouts": driverTimeoutsSchema(),
				},
			},
			"eks_with_eksctl": schema.SingleNestedAttribute{
				Description: "The eks_with_eksctl driver",
				Optional:    true,
				Attributes: map[string]schema.Attribute{
					"region": schema.StringAttribute{
						Description: "The AWS region to use for the eks_with_eksctl driver (default is us-west-2)",
						Optional:    true,
					},
					"kubernetes_version": schema.StringAttribute{
						Description: "The Kubernetes minor version of the EKS control plane, e.g. '1.32' (default is the eksctl default version). Nodegroups follow the control plane version.",
						Optional:    true,
					},
					"node_ami": schema.StringAttribute{
						Description: "The AMI to use for the eks_with_eksctl driver (default is the latest EKS optimized AMI)",
						Optional:    true,
					},
					"node_count": schema.Int64Attribute{
						Description: "The number of nodes to use for the eks_with_eksctl driver (default is 1)",
						Optional:    true,
					},
					"node_type": schema.StringAttribute{
						Description: "The instance type to use for the eks_with_eksctl driver (default is m5.large)",
						Optional:    true,
					},
					"timeouts": driverTimeoutsSchema(),
					"storage": schema.SingleNestedAttribute{
						Description: "Storage configuration for the eks_with_eksctl driver",
						Optional:    true,
						Attributes: map[string]schema.Attribute{
							"size": schema.StringAttribute{
								Description: "The size of the storage volume (e.g., '20Gi')",
								Optional:    true,
							},
							"type": schema.StringAttribute{
								Description: "The type of storage to use (e.g., 'gp2', 'gp3')",
								Optional:    true,
							},
						},
					},
					"pod_identity_associations": schema.ListNestedAttribute{
						Description: "Pod Identity Associations for the EKS driver",
						Optional:    true,
						NestedObject: schema.NestedAttributeObject{
							Attributes: map[string]schema.Attribute{
								"permission_policy_arn": schema.StringAttribute{
									Description: "ARN of the permission policy",
									Optional:    true,
								},
								"service_account_name": schema.StringAttribute{
									Description: "Name of the Kubernetes service account",
									Optional:    true,
								},
								"namespace": schema.StringAttribute{
									Description: "Kubernetes namespace of the service account",
									Optional:    true,
								},
							},
						},
					},
					"aws_profile": schema.StringAttribute{
						Description: "The AWS CLI profile to use for eksctl and AWS CLI commands",
						Optional:    true,
					},
					"tags": schema.MapAttribute{
						Description: "Additional tags to apply to all AWS resources created by the driver. Auto-generated tags (imagetest, imagetest:test-name, imagetest:cluster-name) are always included.",
						ElementType: types.StringType,
						Optional:    true,
					},
				},
			},
			"ec2": driverResourceSchemaEC2,
			"ssh": driverResourceSchemaSSH,
		},
	}
}

var driverResourceSchemaSSH = schema.SingleNestedAttribute{
	Description: "The ssh driver. Runs tests on a host that already exists, over SSH. " +
		"The host runs Docker; the driver pulls the images under test into its daemon with the caller's registry credentials before the tests start, runs each test container under a unique name, and removes the containers at teardown. " +
		"Every container it creates carries the per-run label `imagetest:test-name=<run>`, and the test container gets `IMAGETEST_RUN_LABEL` (the key) and `IMAGETEST_RUN` (the value) in its environment, so a test that starts containers through the Docker socket can pass `--label \"$IMAGETEST_RUN_LABEL=$IMAGETEST_RUN\"` and teardown removes them with the run. " +
		"A failed first SSH connection is reported with a message starting `imagetest: ssh connection failed: `, so a caller can tell a connection that failed from a test that failed. " +
		"With `lock_path`, the wait for the host lock runs before setup and does not count against the resource `timeout`. " +
		"It creates no infrastructure and builds no cloud clients.",
	Optional: true,
	Attributes: map[string]schema.Attribute{
		"host": schema.StringAttribute{
			Description: "Address of the host: a hostname, IPv4 or IPv6 address.",
			Required:    true,
		},
		"ssh_port": schema.Int64Attribute{
			Description: "SSH port for connecting to the host (default: 22).",
			Optional:    true,
		},
		"ssh_user": schema.StringAttribute{
			Description: "SSH user for connecting to the host (default: ubuntu).",
			Optional:    true,
		},
		"ssh_key": schema.StringAttribute{
			Description: "Path to the SSH private key file.",
			Required:    true,
		},
		"known_hosts": schema.StringAttribute{
			Description: "Path to an OpenSSH known_hosts file with the host's key, as written by `ssh-keyscan -p <port> -t ed25519 <host>`; the entry must name `host` as given, as `[host]:port` when the port is not 22. " +
				"Every SSH connection verifies the host key against it and fails on a mismatch. Required: the host is reached by address and every image pull sends the caller's registry credentials over the tunnel.",
			Required: true,
		},
		"shell": schema.StringAttribute{
			Description: "Shell to use for commands (default: bash).",
			Optional:    true,
		},
		"setup_commands": schema.ListAttribute{
			Description: "Commands to run on the host before tests.",
			ElementType: types.StringType,
			Optional:    true,
		},
		"env": schema.MapAttribute{
			Description: "Environment variables for setup commands and container.",
			ElementType: types.StringType,
			Optional:    true,
		},
		"volume_mounts": schema.ListAttribute{
			Description: "Volume mounts for the test container (format: src:dst).",
			ElementType: types.StringType,
			Optional:    true,
		},
		"device_mounts": schema.ListAttribute{
			Description: "Device mounts for the test container (format: src:dst).",
			ElementType: types.StringType,
			Optional:    true,
		},
		"gpus": schema.StringAttribute{
			Description: "GPUs to mount in the test container. Use 'all' for all GPUs, or a number like '1' or '2'.",
			Optional:    true,
		},
		"on_failure": schema.ListAttribute{
			Description: "Commands to run on the host when setup or a test fails, before teardown, for diagnostic collection. " +
				"Unlike a test's `on_failure`, which runs inside the test container, these run on the host itself, in `shell` with `env` like `setup_commands`, and also when setup fails. " +
				"Each command runs independently (best-effort) with a 1m timeout, longer than a test's `on_failure` (10s) because host logs can be large; failures do not prevent subsequent commands from executing. " +
				"Their output is reported as a warning, capped at 2 MiB in total; trim large logs with e.g. `tail -n` or `journalctl -n`. " +
				"With resource-level `retry`, they run only after the last attempt, or after an attempt that ran out of time.",
			ElementType: types.StringType,
			Optional:    true,
		},
		"lock_path": schema.StringAttribute{
			Description: "Path of a file on the host to take an exclusive `flock` on for the length of the run, so runs that share the host run one at a time. " +
				"The driver holds it in an SSH session that stays open until teardown. The wait for a busy lock runs before setup and outside the resource `timeout`, which starts once the lock is held; it is bounded by `lock_timeout` alone and recorded as the span event `ssh.lock.acquired`. " +
				"The directory must let `ssh_user` create or open the file, and `flock` (util-linux) must be on the host. " +
				"Every run on the host must set the same `lock_path`: while the lock is held, setup also removes every container left by an earlier run that held this lock (label `imagetest:lock` with this path, and the containers carrying that run's `imagetest:test-name`), since only the lock holder can have live containers. Runs on no lock or on another lock are left alone. " +
				"The lock is released when the holder's SSH connection ends, but sshd only notices a guest that vanished once TCP keepalive gives up, about two hours with Linux defaults. " +
				"Set `ClientAliveInterval 15` and `ClientAliveCountMax 4` in the host's sshd_config so a dead holder is dropped in about a minute; the driver answers the probes, so live runs are not affected.",
			Optional: true,
		},
		"lock_timeout": schema.StringAttribute{
			Description: "Maximum time to wait for `lock_path` to be free (default: 60m). " +
				"A wait that passes it fails with a message starting `imagetest: ssh lock wait timed out: `, so a caller can tell a host that was busy from a test that failed. " +
				"Ignored without `lock_path`.",
			Optional: true,
		},
		"timeouts": driverTimeoutsSchema(),
	},
}

var driverResourceSchemaEC2 = schema.SingleNestedAttribute{
	Description: "The AWS EC2 driver. On instances the driver launches, images in private ECR registries are pulled with the instance profile, which needs docker-credential-ecr-login on the instance. Install it with `user_data` or `setup_commands`.",
	Optional:    true,
	Attributes: map[string]schema.Attribute{
		"vpc_id": schema.StringAttribute{
			Description: "The VPC ID to create resources in. Required unless using existing_instance (deprecated). With DNS hostnames enabled on the VPC, the driver connects to the instance by its public DNS name when that name resolves to the instance's public IP, otherwise by its public IP.",
			Optional:    true,
		},
		"region": schema.StringAttribute{
			Description: "The AWS region (default: us-west-2).",
			Optional:    true,
		},
		"ami": schema.StringAttribute{
			Description: "The AMI ID to launch. Required unless using existing_instance (deprecated).",
			Optional:    true,
		},
		"instance_type": schema.StringAttribute{
			Description: "The EC2 instance type (default: t3.medium).",
			Optional:    true,
		},
		"root_volume_size": schema.Int64Attribute{
			Description: "Root volume size in GB (default: 50).",
			Optional:    true,
		},
		"instance_profile_name": schema.StringAttribute{
			Description: "IAM instance profile name. If not specified, one is created with ECR read-only permissions. ECR images are pulled with this profile, so a custom profile needs ECR read access, and cross-account repositories must allow it.",
			Optional:    true,
		},
		"subnet_cidr": schema.StringAttribute{
			Description: "The CIDR block for the subnet. If not specified, an available /24 is auto-detected.",
			Optional:    true,
		},
		"ssh_user": schema.StringAttribute{
			Description: "SSH user for connecting to the instance (default: ubuntu).",
			Optional:    true,
		},
		"ssh_port": schema.Int64Attribute{
			Description: "SSH port for connecting to the instance (default: 22).",
			Optional:    true,
		},
		"shell": schema.StringAttribute{
			Description: "Shell to use for commands (default: bash).",
			Optional:    true,
		},
		"setup_commands": schema.ListAttribute{
			Description: "Commands to run on the instance before tests.",
			ElementType: types.StringType,
			Optional:    true,
		},
		"env": schema.MapAttribute{
			Description: "Environment variables for setup commands and container.",
			ElementType: types.StringType,
			Optional:    true,
		},
		"user_data": schema.StringAttribute{
			Description: "Cloud-init user data (will be base64 encoded).",
			Optional:    true,
		},
		"volume_mounts": schema.ListAttribute{
			Description: "Volume mounts for the test container (format: src:dst).",
			ElementType: types.StringType,
			Optional:    true,
		},
		"device_mounts": schema.ListAttribute{
			Description: "Device mounts for the test container (format: src:dst).",
			ElementType: types.StringType,
			Optional:    true,
		},
		"gpus": schema.StringAttribute{
			Description: "GPUs to mount in the test container. Use 'all' for all GPUs, or a number like '1' or '2'.",
			Optional:    true,
		},
		"mount_all_gpus": schema.BoolAttribute{
			Description:        "Deprecated: use gpus = 'all' instead.",
			Optional:           true,
			DeprecationMessage: "Use gpus = 'all' instead.",
		},
		"existing_instance": schema.SingleNestedAttribute{
			Description:        "Deprecated: use the `ssh` driver, which runs tests on an existing host without AWS clients and requires a pinned host key. Use an existing instance instead of creating new resources.",
			DeprecationMessage: "Use the ssh driver instead.",
			Optional:           true,
			Attributes: map[string]schema.Attribute{
				"ip": schema.StringAttribute{
					Description: "IP address of the existing instance.",
					Required:    true,
				},
				"ssh_key": schema.StringAttribute{
					Description: "Path to the SSH private key file.",
					Required:    true,
				},
				"known_hosts": schema.StringAttribute{
					Description: "Path to an OpenSSH known_hosts file with the instance's host key (as written by `ssh-keyscan -t ed25519 <ip>`). When set, every SSH connection verifies the host key against it and fails on a mismatch. When unset, host keys are not checked.",
					Optional:    true,
				},
			},
		},
		"on_failure": schema.ListAttribute{
			Description: "Commands to run on the instance when setup or a test fails, before teardown, for diagnostic collection. " +
				"Unlike a test's `on_failure`, which runs inside the test container, these run on the instance itself, in `shell` with `env` like `setup_commands`, and also when setup fails. " +
				"Each command runs independently (best-effort) with a 1m timeout, longer than a test's `on_failure` (10s) because host logs can be large; failures do not prevent subsequent commands from executing. " +
				"Their output is reported as a warning, capped at 2 MiB in total; trim large logs with e.g. `tail -n` or `journalctl -n`. " +
				"With resource-level `retry`, they run only after the last attempt, or after an attempt that ran out of time.",
			ElementType: types.StringType,
			Optional:    true,
		},
		"timeouts": driverTimeoutsSchema(),
	},
}

// https://github.com/google/go-containerregistry/blob/098045d5e61ff426a61a0eecc19ad0c433cd35a9/pkg/name/registry.go
func isLocalRegistry(ref name.Registry) bool {
	if strings.HasPrefix(ref.Name(), "localhost:") {
		return true
	}
	if reLocal.MatchString(ref.Name()) {
		return true
	}
	if reLoopback.MatchString(ref.Name()) {
		return true
	}
	if reipv6Loopback.MatchString(ref.Name()) {
		return true
	}
	return false
}

// Detect more complex forms of local references.
var reLocal = regexp.MustCompile(`.*\.local(?:host)?(?::\d{1,5})?$`)

// Detect the loopback IP (127.0.0.1).
var reLoopback = regexp.MustCompile(regexp.QuoteMeta("127.0.0.1"))

// Detect the loopback IPV6 (::1).
var reipv6Loopback = regexp.MustCompile(regexp.QuoteMeta("::1"))
