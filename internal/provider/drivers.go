package provider

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
	dockerindocker "github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers/docker_in_docker"
	ec2d "github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers/ec2"
	ekswitheksctl "github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers/eks_with_eksctl"
	k3sindocker "github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers/k3s_in_docker"
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
	EC2            *ec2d.Driver                       `tfsdk:"ec2"`
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
	Region   types.String `tfsdk:"region"`
	NodeAMI  types.String `tfsdk:"node_ami"`
	NodeType types.String `tfsdk:"node_type"`
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
			opts = append(opts, k3sindocker.WithRegistryMirror(t.repo.RegistryStr(), fmt.Sprintf("http://host.docker.internal:%s", parts[1])))
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
		return ekswitheksctl.NewDriver(id, ekswitheksctl.Options{
			Region:   drivers.EKSWithEksctl.Region.ValueString(),
			NodeAMI:  drivers.EKSWithEksctl.NodeAMI.ValueString(),
			NodeType: drivers.EKSWithEksctl.NodeType.ValueString(),
		})

	case DriverEC2:
		// Init a default AWS config
		cfg, err := config.LoadDefaultConfig(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to load default AWS config: %s", err)
		}

		// Init an EC2 client, assign it to the driver instance
		client := ec2.NewFromConfig(cfg)

		if drivers.EC2 == nil {
			// If we received no inbound driver configuration, use the default
			driver := ec2d.DriverDefault
			driver.SetClient(client)
			return driver, nil

		} else {
			// If we received inbound driver configuration, use that
			drivers.EC2.SetClient(client)
			return drivers.EC2, nil
		}

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
			"ec2": schema.SingleNestedAttribute{
				Description: "The Amazon AWS EC2 driver.",
				Optional:    true,
				Attributes: map[string]schema.Attribute{
					"ami": schema.StringAttribute{
						Description: "The Amazon Machine Image (AMI) to use with this instance.",
						Required:    true,
					},
					"instance_type": schema.StringAttribute{
						Description: "The desired EC2 instance type. NOTE: If this is " +
							"provided it will supersede all other instance type constraints.",
						Optional: true,
					},
					"free_tier_eligible": schema.BoolAttribute{
						Description: "If set, the selected instance type must be free-tier eligible.",
						Optional:    true,
					},
					"proc": schema.SingleNestedAttribute{
						Description: "Constraints for the EC2 instance's processor",
						Attributes: map[string]schema.Attribute{
							"arch": schema.StringAttribute{
								Description: "The desired EC2 instance processor architecture.",
								Optional:    true,
							},
							"vcpus": schema.Int32Attribute{
								Description: "The desired number of virtual processors for the EC2 instance.",
								Optional:    true,
							},
						},
					},
					"memory": schema.SingleNestedAttribute{
						Description: "Constraints for the EC2 instance's memory configuration.",
						Attributes: map[string]schema.Attribute{
							"capacity": schema.StringAttribute{
								Description: "The desired instance memory capacity. Can be " +
									"entered as any of 'GB', 'MB', 'KB', 'GiB', 'MiB', 'KiB'. " +
									"Examples: '4gb', '4GB', '4000MB', '3192mIb'. " +
									"Capacity units are not case sensitive. " +
									"If a capacity unit is not specified (ex: '4') gigabytes " +
									"are assumed.",
								Required: false,
							},
						},
					},
					"disks": schema.ListNestedAttribute{
						Description: "Constraints for the EC2 instance's storage configuration.",
						NestedObject: schema.NestedAttributeObject{
							Attributes: map[string]schema.Attribute{
								"kind": schema.StringAttribute{
									Description: "The EC2 instance disk kind. Can be one of: " +
										"'hdd', 'ssd' or 'nvme'.",
									Optional: true,
								},
								"capacity": schema.Int32Attribute{
									Description: "The desired EC2 instance disk capacity (in GB).",
									Optional:    true,
								},
								"nvme_support": schema.BoolAttribute{
									Description: "Whether the EC2 instance supports NVME disks.",
									Optional:    true,
								},
							},
						},
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
					"node_type": schema.StringAttribute{
						Description: "The instance type to use for the eks_with_eksctl driver (default is m5.large)",
						Optional:    true,
					},
				},
			},
		},
	}
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
