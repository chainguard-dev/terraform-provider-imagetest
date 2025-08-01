package provider

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
	dockerindocker "github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers/docker_in_docker"
	mc2 "github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers/ec2"
	ekswitheksctl "github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers/eks_with_eksctl"
	k3sindocker "github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers/k3s_in_docker"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/ssh"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type DriverResourceModel string

const (
	DriverK3sInDocker    DriverResourceModel = "k3s_in_docker"
	DriverDockerInDocker DriverResourceModel = "docker_in_docker"
	DriverEKSWithEksctl  DriverResourceModel = "eks_with_eksctl"
	DriverEC2            DriverResourceModel = "ec2"
)

type TestsDriversResourceModel struct {
	K3sInDocker    *K3sInDockerDriverResourceModel    `tfsdk:"k3s_in_docker"`
	DockerInDocker *DockerInDockerDriverResourceModel `tfsdk:"docker_in_docker"`
	EKSWithEksctl  *EKSWithEksctlDriverResourceModel  `tfsdk:"eks_with_eksctl"`
	EC2            *EC2DriverResourceModel            `tfsdk:"ec2"`
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
	Image   types.String `tfsdk:"image"`
	Mirrors []string     `tfsdk:"mirrors"`
}

type EKSWithEksctlDriverResourceModel struct {
	Region                  types.String                                         `tfsdk:"region"`
	NodeAMI                 types.String                                         `tfsdk:"node_ami"`
	NodeType                types.String                                         `tfsdk:"node_type"`
	NodeCount               types.Int64                                          `tfsdk:"node_count"`
	Storage                 *EKSWithEksctlStorageResourceModel                   `tfsdk:"storage"`
	PodIdentityAssociations []*EKSWithEksctlPodIdentityAssociationResourceModule `tfsdk:"pod_identity_associations"`
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
	Region       types.String               `tfsdk:"region"`
	AMI          types.String               `tfsdk:"ami"`
	InstanceType types.String               `tfsdk:"instance_type"`
	Exec         EC2DriverExecResourceModel `tfsdk:"exec"`
	VolumeMounts []types.String             `tfsdk:"volume_mounts"`
	DeviceMounts []types.String             `tfsdk:"device_mounts"`
	MountAllGPUs types.Bool                 `tfsdk:"mount_all_gpus"`
}

type EC2DriverExecResourceModel struct {
	User     types.String   `tfsdk:"user"`
	Shell    types.String   `tfsdk:"shell"`
	Commands []types.String `tfsdk:"commands"`
	Env      types.Map      `tfsdk:"env"`
	UserData types.String   `tfsdk:"user_data"`
}

func (t TestsResource) LoadDriver(ctx context.Context, drivers *TestsDriversResourceModel, driver DriverResourceModel, id string) (drivers.Tester, error) {
	if drivers == nil {
		drivers = &TestsDriversResourceModel{}
	}

	switch driver {
	case DriverK3sInDocker:
		cfg := drivers.K3sInDocker
		if cfg == nil {
			cfg = &K3sInDockerDriverResourceModel{}
		}

		opts := []k3sindocker.DriverOpts{
			k3sindocker.WithRegistry(t.repo.RegistryStr()),
		}

		for _, repo := range t.extraRepos {
			opts = append(opts, k3sindocker.WithRegistry(repo.RegistryStr()))
		}

		tf, err := os.CreateTemp("", "imagetest-k3s-in-docker")
		if err != nil {
			return nil, err
		}
		opts = append(opts, k3sindocker.WithWriteKubeconfig(tf.Name()))

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

# Restart CoreDNS pods to immediately load the new configuration # NOTE:
CoreDNS has no _good_ way to validate the configuration has reloaded. This
looks ugly, but in practice its the cheapest reliable way to ensure the new
configuration is loaded, and only takes a few seconds since the image is
already pulled.
kubectl rollout restart deployment/coredns -n kube-system
kubectl rollout status deployment/coredns -n kube-system --timeout=60s
`
			opts = append(opts, k3sindocker.WithPostStartHook(coreDNSHook))
		}

		return k3sindocker.NewDriver(id, opts...)

	case DriverDockerInDocker:
		cfg := drivers.DockerInDocker
		if cfg == nil {
			cfg = &DockerInDockerDriverResourceModel{}
		}

		opts := []dockerindocker.DriverOpts{
			dockerindocker.WithRemoteOptions(t.ropts...),
			dockerindocker.WithRegistryAuth(t.repo.RegistryStr()),
		}

		for _, repo := range t.extraRepos {
			opts = append(opts, dockerindocker.WithRegistryAuth(repo.RegistryStr()))
		}

		if cfg.Image.ValueString() != "" {
			opts = append(opts, dockerindocker.WithImageRef(cfg.Image.ValueString()))
		}

		if len(cfg.Mirrors) > 0 {
			opts = append(opts, dockerindocker.WithRegistryMirrors(cfg.Mirrors...))
		}

		if isLocalRegistry(t.repo.Registry) {
			u, err := url.Parse("http://" + t.repo.RegistryStr())
			if err != nil {
				return nil, fmt.Errorf("failed to parse registry url: %w", err)
			}

			opts = append(opts,
				dockerindocker.WithExtraHosts(
					fmt.Sprintf("%s:%s", u.Hostname(), "127.0.0.1"),
				),
			)
		}

		return dockerindocker.NewDriver(id, opts...)

	case DriverEKSWithEksctl:
		cfg := drivers.EKSWithEksctl
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

		return ekswitheksctl.NewDriver(id, ekswitheksctl.Options{
			Region:                  cfg.Region.ValueString(),
			NodeAMI:                 cfg.NodeAMI.ValueString(),
			NodeType:                cfg.NodeType.ValueString(),
			NodeCount:               int(cfg.NodeCount.ValueInt64()),
			PodIdentityAssociations: podIdentityAssociations,
			Storage:                 storageOpts,
		})

	case DriverEC2:
		// This driver can't do its job without user input.
		if drivers.EC2 == nil {
			return nil, fmt.Errorf(
				"the EC2 driver was specified, but no configuration was provided",
			)
		}

		// Init a default AWS config.
		cfg, err := config.LoadDefaultConfig(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to load AWS configuration: %w", err)
		}

		// Init an EC2 client.
		client := ec2.NewFromConfig(cfg)

		// Init the EC2 driver.
		d, err := mc2.NewDriver(client)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize EC2 driver: %w", err)
		}

		// Capture basic string inputs.
		d.InstanceType = ec2types.InstanceType(drivers.EC2.InstanceType.ValueString())
		d.AMI = drivers.EC2.AMI.ValueString()
		d.Region = drivers.EC2.Region.ValueString()

		// Capture volume mounts.
		for _, mount := range drivers.EC2.VolumeMounts {
			d.VolumeMounts = append(d.VolumeMounts, mount.ValueString())
		}

		// Capture device mounts.
		for _, mount := range drivers.EC2.DeviceMounts {
			d.DeviceMounts = append(d.DeviceMounts, mount.ValueString())
		}

		// Evaluate for GPU passthrough.
		d.MountAllGPUs = drivers.EC2.MountAllGPUs.ValueBool()

		// Translate all user-provided 'exec' objects to 'internal/drivers/ec2.Exec'.
		// Capture 'user'.
		d.Exec.User = drivers.EC2.Exec.User.ValueString()
		if d.Exec.User == "" {
			d.Exec.User = "ubuntu"
		}

		// Capture 'shell'.
		d.Exec.Shell = drivers.EC2.Exec.Shell.ValueString()
		if d.Exec.Shell == "" {
			d.Exec.Shell = ssh.ShellBash
		}

		// Capture any provided environment variables.
		d.Exec.Env = make(map[string]string)
		// Sprinkle on top user provided pairs.
		for k, v := range drivers.EC2.Exec.Env.Elements() {
			if v.IsNull() || v.IsUnknown() {
				continue
			}
			d.Exec.Env[k] = v.String()
		}

		// Capture commands.
		d.Exec.Commands = make([]string, len(drivers.EC2.Exec.Commands))
		for i, cmd := range drivers.EC2.Exec.Commands {
			d.Exec.Commands[i] = cmd.ValueString()
		}

		// Capture any CloudInit userdata.
		if userData := drivers.EC2.Exec.UserData.ValueString(); userData != "" {
			d.Exec.UserData = base64.StdEncoding.EncodeToString([]byte(userData))
		}
		return d, nil

	default:
		return nil, fmt.Errorf("no matching driver: %s", driver)
	}
}

func DriverResourceSchema(ctx context.Context) schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Description: "The resource specific driver configuration. This is merged with the provider scoped drivers configuration.",
		Optional:    true,
		Attributes: map[string]schema.Attribute{
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
				},
			},
			"ec2": driverResourceSchemaEC2,
		},
	}
}

var driverResourceSchemaEC2 = schema.SingleNestedAttribute{
	Description: "The AWS EC2 driver.",
	Optional:    true,
	Attributes: map[string]schema.Attribute{
		"region": schema.StringAttribute{
			Description: "The AWS region to use for the EC2 driver (default is us-west-2).",
			Optional:    true,
		},
		"ami": schema.StringAttribute{
			Description: "The AMI to use for the AMI driver (default is Ubuntu-24.04).",
			Optional:    true,
		},
		"instance_type": schema.StringAttribute{
			Description: "The AWS EC2 instance type to launch (default is TODO).",
			Optional:    true,
		},
		"exec": schema.SingleNestedAttribute{
			Description: "Comamnds to execute on the EC2 instance after launch.",
			Optional:    true,
			Attributes: map[string]schema.Attribute{
				"shell": schema.StringAttribute{
					Optional: true,
				},
				"user": schema.StringAttribute{
					Optional: true,
				},
				"env": schema.MapAttribute{
					ElementType: types.StringType,
					Optional:    true,
				},
				"commands": schema.ListAttribute{
					ElementType: types.StringType,
					Optional:    true,
				},
				"user_data": schema.StringAttribute{
					Optional: true,
				},
			},
		},
		"volume_mounts": schema.ListAttribute{
			ElementType: types.StringType,
			Optional:    true,
		},
		"device_mounts": schema.ListAttribute{
			ElementType: types.StringType,
			Optional:    true,
		},
		"mount_all_gpus": schema.BoolAttribute{
			Optional: true,
		},
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
