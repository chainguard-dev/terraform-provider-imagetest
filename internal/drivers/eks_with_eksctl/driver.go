package ekswitheksctl

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/drivers/pod"
	"github.com/charmbracelet/log"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/uuid"
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
	name      string
	nodeAMI   string
	nodeType  string
	nodeCount int
	storage   *StorageOptions

	region           string
	clusterName      string
	namespace        string
	kubeconfig       string
	kcli             kubernetes.Interface
	kcfg             *rest.Config
	ec2Client        *ec2.Client
	launchTemplate   string
	launchTemplateId string
	nodeGroup        string

	podIdentityAssociations []*podIdentityAssociation
}

type Options struct {
	Region                  string
	NodeType                string
	NodeAMI                 string
	NodeCount               int
	Namespace               string
	Storage                 *StorageOptions
	PodIdentityAssociations []*PodIdentityAssociationOptions
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

func NewDriver(name string, opts Options) (drivers.Tester, error) {
	k := &driver{
		name:      name,
		region:    opts.Region,
		nodeAMI:   opts.NodeAMI,
		nodeType:  opts.NodeType,
		nodeCount: opts.NodeCount,
		namespace: opts.Namespace,
		storage:   opts.Storage,
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

	if _, err := exec.LookPath("eksctl"); err != nil {
		return nil, fmt.Errorf("eksctl not found in $PATH: %w", err)
	}
	return k, nil
}

func (k *driver) eksctl(ctx context.Context, args ...string) error {
	args = append(args, []string{
		"--color", "false", // Disable color output
	}...)
	clog.FromContext(ctx).Infof("eksctl %v", args)
	cmd := exec.CommandContext(ctx, "eksctl", args...)
	cmd.Env = os.Environ() // Copy the environment
	cmd.Env = append(cmd.Env, "KUBECONFIG="+k.kubeconfig)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("eksctl %v: %v: %s", args, err, out)
	}
	return nil
}

func (k *driver) createLaunchTemplate(ctx context.Context) error {
	log := clog.FromContext(ctx)

	templateName := fmt.Sprintf("imagetest-%s", uuid.New().String())
	k.launchTemplate = templateName

	blockDeviceMappings := []ec2types.LaunchTemplateBlockDeviceMappingRequest{
		// Root volume
		{
			DeviceName: aws.String("/dev/xvda"),
			Ebs: &ec2types.LaunchTemplateEbsBlockDeviceRequest{
				DeleteOnTermination: aws.Bool(true),
				VolumeType:          ec2types.VolumeTypeGp3,
				VolumeSize:          aws.Int32(80),
			},
		},
	}

	// Add secondary volume if storage options are provided
	if k.storage != nil && k.storage.Size != "" {
		var sizeGB int

		_, err := fmt.Sscanf(k.storage.Size, "%dGB", &sizeGB)
		if err != nil {
			return fmt.Errorf("failed to parse storage size '%s': %w", k.storage.Size, err)
		}

		// Default to gp3 volume type if not specified
		volumeType := ec2types.VolumeTypeGp3
		if k.storage.Type != "" {
			volumeType = ec2types.VolumeType(k.storage.Type)
		}

		log.Infof("Adding secondary volume: %dGB, type: %s", sizeGB, volumeType)

		blockDeviceMappings = append(blockDeviceMappings, ec2types.LaunchTemplateBlockDeviceMappingRequest{
			DeviceName: aws.String("/dev/xvdb"),
			Ebs: &ec2types.LaunchTemplateEbsBlockDeviceRequest{
				DeleteOnTermination: aws.Bool(true),
				VolumeSize:          aws.Int32(int32(sizeGB)),
				VolumeType:          volumeType,
			},
		})
	}

	input := &ec2.CreateLaunchTemplateInput{
		LaunchTemplateName: aws.String(templateName),
		VersionDescription: aws.String("Created by imagetest"),
		LaunchTemplateData: &ec2types.RequestLaunchTemplateData{
			InstanceType:        ec2types.InstanceType(k.nodeType),
			BlockDeviceMappings: blockDeviceMappings,
		},
		TagSpecifications: []ec2types.TagSpecification{
			{
				ResourceType: ec2types.ResourceTypeLaunchTemplate,
				Tags: []ec2types.Tag{
					{
						Key:   aws.String("Name"),
						Value: aws.String(templateName),
					},
					{
						Key:   aws.String("CreatedBy"),
						Value: aws.String("imagetest"),
					},
				},
			},
		},
	}

	// Set AMI ID if provided
	if k.nodeAMI != "" {
		input.LaunchTemplateData.ImageId = aws.String(k.nodeAMI)
	}

	result, err := k.ec2Client.CreateLaunchTemplate(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to create launch template: %w", err)
	}

	k.launchTemplate = templateName
	k.launchTemplateId = *result.LaunchTemplate.LaunchTemplateId

	log.Infof("Created launch template: %s (ID: %s)", *result.LaunchTemplate.LaunchTemplateName, *result.LaunchTemplate.LaunchTemplateId)
	return nil
}

func (k *driver) deleteLaunchTemplate(ctx context.Context) error {
	if k.launchTemplate == "" {
		return nil
	}

	log := clog.FromContext(ctx)

	_, err := k.ec2Client.DeleteLaunchTemplate(ctx, &ec2.DeleteLaunchTemplateInput{
		LaunchTemplateName: aws.String(k.launchTemplate),
	})
	if err != nil {
		return fmt.Errorf("failed to delete launch template %s: %w", k.launchTemplate, err)
	}

	log.Infof("Deleted launch template: %s", k.launchTemplate)
	return nil
}

func (k *driver) createNodeGroup(ctx context.Context) error {
	log := clog.FromContext(ctx)

	nodeGroupName := fmt.Sprintf("ng-%s", uuid.New().String())
	k.nodeGroup = nodeGroupName // Store the nodegroup name for later deletion

	// Create a temporary file for the eksctl config
	configFile, err := os.CreateTemp("", "eksctl-config-*.yaml")
	if err != nil {
		return fmt.Errorf("failed to create temporary config file: %w", err)
	}
	defer os.Remove(configFile.Name())

	configTemplate := `apiVersion: eksctl.io/v1alpha5
kind: ClusterConfig
metadata:
  name: %s
  region: %s
managedNodeGroups:
- name: %s
  desiredCapacity: %d
  launchTemplate:
    id: %s
    version: "1"
`
	configContent := fmt.Sprintf(
		configTemplate,
		k.clusterName,
		k.region,
		k.nodeGroup,
		k.nodeCount,
		k.launchTemplateId,
	)

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

func (k *driver) deleteNodeGroup(ctx context.Context) error {
	if k.nodeGroup == "" {
		return nil
	}

	log := clog.FromContext(ctx)

	if err := k.eksctl(ctx, "delete", "nodegroup", "--region="+k.region, "--cluster="+k.clusterName, "--name="+k.nodeGroup, "--disable-eviction"); err != nil {
		return fmt.Errorf("eksctl delete nodegroup: %w", err)
	}

	log.Infof("Deleted nodegroup %s from cluster %s", k.nodeGroup, k.clusterName)
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
			v.namespace, v.serviceAccountName, k.nodeCount, v.permissionPolicyARN, k.clusterName)
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
			v.namespace, v.serviceAccountName, k.nodeCount, v.permissionPolicyARN, k.clusterName)
	}

	return nil
}

func (k *driver) Setup(ctx context.Context) error {
	log := clog.FromContext(ctx)

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

	awsCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(k.region))
	if err != nil {
		return fmt.Errorf("failed to load AWS config: %w", err)
	}
	k.ec2Client = ec2.NewFromConfig(awsCfg)

	usingExistingCluster := false
	if _, ok := os.LookupEnv("IMAGETEST_EKS_CLUSTER"); ok {
		if err := k.eksctl(ctx, "utils", "write-kubeconfig", "--cluster", k.clusterName, "--kubeconfig", "--region", k.kubeconfig); err != nil {
			return fmt.Errorf("eksctl utils write-kubeconfig: %w", err)
		}
		usingExistingCluster = true
	}

	if err := k.createLaunchTemplate(ctx); err != nil {
		return err
	}

	if !usingExistingCluster {
		args := []string{
			"create", "cluster",
			"--node-private-networking=false",
			"--region=" + k.region,
			"--vpc-nat-mode=Disable",
			"--kubeconfig=" + k.kubeconfig,
			"--name=" + k.clusterName,
			"--without-nodegroup",
		}

		if err := k.eksctl(ctx, args...); err != nil {
			return fmt.Errorf("eksctl create cluster: %w", err)
		}
		log.Infof("Created cluster %s without nodegroups", k.clusterName)
	}

	if err := k.createNodeGroup(ctx); err != nil {
		return err
	}

	if k.podIdentityAssociations != nil {
		if err = k.createPodIdentityAssociation(ctx); err != nil {
			return fmt.Errorf("creating pod identity association: %w", err)
		}
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
	if v := os.Getenv("IMAGETEST_EKS_SKIP_TEARDOWN"); v == "true" {
		clog.FromContext(ctx).Info("Skipping EKS teardown due to IMAGETEST_EKS_SKIP_TEARDOWN=true")
		return nil
	}

	if k.nodeGroup != "" {
		if err := k.deleteNodeGroup(ctx); err != nil {
			return err
		}
	}

	if err := k.eksctl(ctx, "delete", "cluster", "--name", k.clusterName); err != nil {
		return fmt.Errorf("eksctl delete cluster: %w", err)
	}

	if k.launchTemplate != "" {
		if err := k.deleteLaunchTemplate(ctx); err != nil {
			return err
		}
	}

	if k.podIdentityAssociations != nil {
		if err := k.deletePodIdentityAssociation(ctx); err != nil {
			return fmt.Errorf("deleting pod identity association: %w", err)
		}
	}

	return nil
}

func (k *driver) Run(ctx context.Context, ref name.Reference) (*drivers.RunResult, error) {
	return pod.Run(ctx, k.kcfg,
		pod.WithImageRef(ref),
		pod.WithExtraEnvs(map[string]string{
			"IMAGETEST_DRIVER": "eks_with_eksctl",
		}),
	)
}
