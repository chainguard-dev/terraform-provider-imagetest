package provider

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
	dockerindocker "github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers/docker_in_docker"
	ekswitheksctl "github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers/eks_with_eksctl"
	k3sindocker "github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers/k3s_in_docker"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers/lambda"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type DriverResourceModel string

const (
	DriverK3sInDocker    DriverResourceModel = "k3s_in_docker"
	DriverDockerInDocker DriverResourceModel = "docker_in_docker"
	DriverEKSWithEksctl  DriverResourceModel = "eks_with_eksctl"
	DriverLambda         DriverResourceModel = "lambda"
)

type TestsDriversResourceModel struct {
	K3sInDocker    *K3sInDockerDriverResourceModel    `tfsdk:"k3s_in_docker"`
	DockerInDocker *DockerInDockerDriverResourceModel `tfsdk:"docker_in_docker"`
	EKSWithEksctl  *EKSWithEksctlDriverResourceModel  `tfsdk:"eks_with_eksctl"`
	Lambda         *LambdaDriverResourceModel         `tfsdk:"lambda"`
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

type EKSWithEksctlDriverResourceModel struct{}

type LambdaDriverResourceModel struct{}

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
		return ekswitheksctl.NewDriver(id)

	case DriverLambda:
		return lambda.NewDriver(id)

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
				Attributes:  map[string]schema.Attribute{},
			},
			"lambda": schema.SingleNestedAttribute{
				Description: "The lambda driver",
				Optional:    true,
				Attributes:  map[string]schema.Attribute{},
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
