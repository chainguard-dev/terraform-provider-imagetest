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

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/docker"
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
	name       string
	nodeAMI    string
	nodeType   string
	nodeCount  int
	storage    *StorageOptions
	awsProfile string
	tags       map[string]string
	timeout    string // Go duration format for eksctl --timeout flag

	region           string
	clusterName      string
	namespace        string
	kubeconfig       string
	kcli             kubernetes.Interface
	kcfg             *rest.Config
	ec2Client        *ec2.Client
	eksClient        *eks.Client
	launchTemplate   string
	launchTemplateId string
	nodeGroup        string
	vpcId            string

	podIdentityAssociations []*podIdentityAssociation
	registries              map[string]*RegistryConfig
}

type Options struct {
	Region                  string
	NodeType                string
	NodeAMI                 string
	NodeCount               int
	Namespace               string
	Storage                 *StorageOptions
	PodIdentityAssociations []*PodIdentityAssociationOptions
	AWSProfile              string
	Tags                    map[string]string
	Timeout                 string // Go duration format (e.g., "30m", "1h") for eksctl --timeout flag
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
// When opts.Timeout is set, it overrides eksctl's default timeout of 25 minutes for all
// long-running operations (cluster creation, node group operations, etc.).
func NewDriver(name string, opts Options) (drivers.Tester, error) {
	k := &driver{
		name:       name,
		region:     opts.Region,
		nodeAMI:    opts.NodeAMI,
		nodeType:   opts.NodeType,
		nodeCount:  opts.NodeCount,
		namespace:  opts.Namespace,
		storage:    opts.Storage,
		awsProfile: opts.AWSProfile,
		tags:       opts.Tags,
		timeout:    opts.Timeout,
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

func (k *driver) eksctl(ctx context.Context, args ...string) error {
	args = append(args, []string{
		"--color", "false", // Disable color output
	}...)

	// Add timeout flag if configured (empty string = use eksctl default of 25m)
	if k.timeout != "" {
		args = append(args, "--timeout", k.timeout)
	}

	clog.FromContext(ctx).Infof("eksctl %v", args)
	cmd := exec.CommandContext(ctx, "eksctl", args...)
	cmd.Env = os.Environ() // Copy the environment
	cmd.Env = append(cmd.Env, "KUBECONFIG="+k.kubeconfig)
	if k.awsProfile != "" {
		cmd.Env = append(cmd.Env, "AWS_PROFILE="+k.awsProfile)
	}
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

	ec2Tags := []ec2types.Tag{
		{Key: aws.String("Name"), Value: aws.String(templateName)},
	}
	for key, value := range k.buildTags() {
		ec2Tags = append(ec2Tags, ec2types.Tag{Key: aws.String(key), Value: aws.String(value)})
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
				Tags:         ec2Tags,
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

func (k *driver) getClusterVpcId(ctx context.Context) error {
	log := clog.FromContext(ctx)

	result, err := k.eksClient.DescribeCluster(ctx, &eks.DescribeClusterInput{
		Name: aws.String(k.clusterName),
	})
	if err != nil {
		return fmt.Errorf("failed to describe cluster %s: %w", k.clusterName, err)
	}

	if result.Cluster != nil && result.Cluster.ResourcesVpcConfig != nil && result.Cluster.ResourcesVpcConfig.VpcId != nil {
		k.vpcId = *result.Cluster.ResourcesVpcConfig.VpcId
		log.Infof("Found VPC ID for cluster %s: %s", k.clusterName, k.vpcId)
	} else {
		log.Warnf("Could not retrieve VPC ID for cluster %s", k.clusterName)
	}

	return nil
}

func (k *driver) cleanupVpc(ctx context.Context) error {
	if k.vpcId == "" {
		return nil
	}

	log := clog.FromContext(ctx)
	log.Infof("Attempting to clean up VPC %s", k.vpcId)

	// Retry loop for VPC cleanup
	maxRetries := 10
	retryDelay := 5 * time.Second

	for attempt := range maxRetries {
		if attempt > 0 {
			log.Infof("Retry attempt %d/%d for VPC cleanup", attempt+1, maxRetries)
			time.Sleep(retryDelay)
		}

		// Step 1: Delete all network interfaces (ENIs)
		if err := k.deleteNetworkInterfaces(ctx); err != nil {
			log.Warnf("Failed to delete network interfaces: %v", err)
			continue
		}

		// Step 2: Delete non-default security groups
		if err := k.deleteSecurityGroups(ctx); err != nil {
			log.Warnf("Failed to delete security groups: %v", err)
			continue
		}

		// Step 3: Delete subnets
		if err := k.deleteSubnets(ctx); err != nil {
			log.Warnf("Failed to delete subnets: %v", err)
			continue
		}

		// Step 4: Detach and delete internet gateways
		if err := k.deleteInternetGateways(ctx); err != nil {
			log.Warnf("Failed to delete internet gateways: %v", err)
			continue
		}

		// Step 5: Delete route tables (except main)
		if err := k.deleteRouteTables(ctx); err != nil {
			log.Warnf("Failed to delete route tables: %v", err)
			continue
		}

		// Step 6: Finally, delete the VPC
		_, err := k.ec2Client.DeleteVpc(ctx, &ec2.DeleteVpcInput{
			VpcId: aws.String(k.vpcId),
		})
		if err != nil {
			log.Warnf("Failed to delete VPC: %v", err)
			continue
		}

		log.Infof("Successfully deleted VPC %s", k.vpcId)
		return nil
	}

	return fmt.Errorf("failed to delete VPC %s after %d retries", k.vpcId, maxRetries)
}

func (k *driver) deleteNetworkInterfaces(ctx context.Context) error {
	log := clog.FromContext(ctx)

	paginator := ec2.NewDescribeNetworkInterfacesPaginator(k.ec2Client, &ec2.DescribeNetworkInterfacesInput{
		Filters: []ec2types.Filter{
			{
				Name:   aws.String("vpc-id"),
				Values: []string{k.vpcId},
			},
		},
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed to describe network interfaces: %w", err)
		}

		for _, eni := range page.NetworkInterfaces {
			if eni.NetworkInterfaceId == nil {
				continue
			}

			// Detach if attached or attaching (skip if already detaching or detached)
			if eni.Attachment != nil &&
				(eni.Attachment.Status == ec2types.AttachmentStatusAttached ||
					eni.Attachment.Status == ec2types.AttachmentStatusAttaching) {
				log.Infof("Detaching ENI %s", *eni.NetworkInterfaceId)
				_, err := k.ec2Client.DetachNetworkInterface(ctx, &ec2.DetachNetworkInterfaceInput{
					AttachmentId: eni.Attachment.AttachmentId,
					Force:        aws.Bool(true),
				})
				if err != nil {
					log.Warnf("Failed to detach ENI %s: %v", *eni.NetworkInterfaceId, err)
				}
				// Wait a bit for detachment to complete
				time.Sleep(2 * time.Second)
			}

			// Delete the ENI
			log.Infof("Deleting ENI %s", *eni.NetworkInterfaceId)
			_, err := k.ec2Client.DeleteNetworkInterface(ctx, &ec2.DeleteNetworkInterfaceInput{
				NetworkInterfaceId: eni.NetworkInterfaceId,
			})
			if err != nil {
				log.Warnf("Failed to delete ENI %s: %v", *eni.NetworkInterfaceId, err)
			}
		}
	}

	return nil
}

func (k *driver) deleteSecurityGroups(ctx context.Context) error {
	log := clog.FromContext(ctx)

	paginator := ec2.NewDescribeSecurityGroupsPaginator(k.ec2Client, &ec2.DescribeSecurityGroupsInput{
		Filters: []ec2types.Filter{
			{
				Name:   aws.String("vpc-id"),
				Values: []string{k.vpcId},
			},
		},
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed to describe security groups: %w", err)
		}

		for _, sg := range page.SecurityGroups {
			if sg.GroupId == nil || sg.GroupName == nil {
				continue
			}

			// Skip default security group
			if *sg.GroupName == "default" {
				continue
			}

			log.Infof("Deleting security group %s (%s)", *sg.GroupName, *sg.GroupId)
			_, err := k.ec2Client.DeleteSecurityGroup(ctx, &ec2.DeleteSecurityGroupInput{
				GroupId: sg.GroupId,
			})
			if err != nil {
				log.Warnf("Failed to delete security group %s: %v", *sg.GroupId, err)
			}
		}
	}

	return nil
}

func (k *driver) deleteSubnets(ctx context.Context) error {
	log := clog.FromContext(ctx)

	paginator := ec2.NewDescribeSubnetsPaginator(k.ec2Client, &ec2.DescribeSubnetsInput{
		Filters: []ec2types.Filter{
			{
				Name:   aws.String("vpc-id"),
				Values: []string{k.vpcId},
			},
		},
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed to describe subnets: %w", err)
		}

		for _, subnet := range page.Subnets {
			if subnet.SubnetId == nil {
				continue
			}

			log.Infof("Deleting subnet %s", *subnet.SubnetId)
			_, err := k.ec2Client.DeleteSubnet(ctx, &ec2.DeleteSubnetInput{
				SubnetId: subnet.SubnetId,
			})
			if err != nil {
				log.Warnf("Failed to delete subnet %s: %v", *subnet.SubnetId, err)
			}
		}
	}

	return nil
}

func (k *driver) deleteInternetGateways(ctx context.Context) error {
	log := clog.FromContext(ctx)

	paginator := ec2.NewDescribeInternetGatewaysPaginator(k.ec2Client, &ec2.DescribeInternetGatewaysInput{
		Filters: []ec2types.Filter{
			{
				Name:   aws.String("attachment.vpc-id"),
				Values: []string{k.vpcId},
			},
		},
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed to describe internet gateways: %w", err)
		}

		for _, igw := range page.InternetGateways {
			if igw.InternetGatewayId == nil {
				continue
			}

			// Detach from VPC first
			log.Infof("Detaching internet gateway %s from VPC %s", *igw.InternetGatewayId, k.vpcId)
			_, err := k.ec2Client.DetachInternetGateway(ctx, &ec2.DetachInternetGatewayInput{
				InternetGatewayId: igw.InternetGatewayId,
				VpcId:             aws.String(k.vpcId),
			})
			if err != nil {
				log.Warnf("Failed to detach internet gateway %s: %v", *igw.InternetGatewayId, err)
			}

			// Delete the internet gateway
			log.Infof("Deleting internet gateway %s", *igw.InternetGatewayId)
			_, err = k.ec2Client.DeleteInternetGateway(ctx, &ec2.DeleteInternetGatewayInput{
				InternetGatewayId: igw.InternetGatewayId,
			})
			if err != nil {
				log.Warnf("Failed to delete internet gateway %s: %v", *igw.InternetGatewayId, err)
			}
		}
	}

	return nil
}

func (k *driver) deleteRouteTables(ctx context.Context) error {
	log := clog.FromContext(ctx)

	paginator := ec2.NewDescribeRouteTablesPaginator(k.ec2Client, &ec2.DescribeRouteTablesInput{
		Filters: []ec2types.Filter{
			{
				Name:   aws.String("vpc-id"),
				Values: []string{k.vpcId},
			},
		},
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed to describe route tables: %w", err)
		}

		for _, rt := range page.RouteTables {
			if rt.RouteTableId == nil {
				continue
			}

			// Skip main route table
			isMain := false
			for _, assoc := range rt.Associations {
				if assoc.Main != nil && *assoc.Main {
					isMain = true
					break
				}
			}
			if isMain {
				continue
			}

			log.Infof("Deleting route table %s", *rt.RouteTableId)
			_, err := k.ec2Client.DeleteRouteTable(ctx, &ec2.DeleteRouteTableInput{
				RouteTableId: rt.RouteTableId,
			})
			if err != nil {
				log.Warnf("Failed to delete route table %s: %v", *rt.RouteTableId, err)
			}
		}
	}

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

	const configTemplate = `apiVersion: eksctl.io/v1alpha5
kind: ClusterConfig
metadata:
  name: {{ .ClusterName }}
  region: {{ .Region }}
managedNodeGroups:
- name: {{ .NodeGroup }}
  desiredCapacity: {{ .NodeCount }}
  amiFamily: {{ .AMIFamily }}
  launchTemplate:
    id: {{ .LaunchTemplateId }}
    version: "1"
  tags:
{{- range $key, $value := .Tags }}
    {{ $key }}: {{ $value | printf "%q" }}
{{- end }}
`

	tmpl, err := template.New("nodegroup").Parse(configTemplate)
	if err != nil {
		return fmt.Errorf("failed to parse nodegroup template: %w", err)
	}

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, map[string]any{
		"ClusterName":      k.clusterName,
		"Region":           k.region,
		"NodeGroup":        k.nodeGroup,
		"NodeCount":        k.nodeCount,
		"AMIFamily":        k.amiFamily(),
		"LaunchTemplateId": k.launchTemplateId,
		"Tags":             k.buildTags(),
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

	awsOpts := []func(*config.LoadOptions) error{config.WithRegion(k.region)}
	if k.awsProfile != "" {
		awsOpts = append(awsOpts, config.WithSharedConfigProfile(k.awsProfile))
	}
	awsCfg, err := config.LoadDefaultConfig(ctx, awsOpts...)
	if err != nil {
		return fmt.Errorf("failed to load AWS config: %w", err)
	}
	k.ec2Client = ec2.NewFromConfig(awsCfg)
	k.eksClient = eks.NewFromConfig(awsCfg)

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

		tags := k.buildTags()
		pairs := make([]string, 0, len(tags))
		for key, value := range tags {
			pairs = append(pairs, key+"="+value)
		}
		args = append(args, "--tags="+strings.Join(pairs, ","))

		if err := k.eksctl(ctx, args...); err != nil {
			return fmt.Errorf("eksctl create cluster: %w", err)
		}
		log.Infof("Created cluster %s without nodegroups", k.clusterName)

		// Retrieve the VPC ID for potential cleanup later
		if err := k.getClusterVpcId(ctx); err != nil {
			log.Warnf("Failed to retrieve VPC ID: %v", err)
		}
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

	// Delete pod identity associations first (before cluster deletion)
	if k.podIdentityAssociations != nil {
		if err := k.deletePodIdentityAssociation(ctx); err != nil {
			return fmt.Errorf("deleting pod identity association: %w", err)
		}
	}

	if k.nodeGroup != "" {
		if err := k.deleteNodeGroup(ctx); err != nil {
			return err
		}
	}

	if err := k.eksctl(ctx, "delete", "cluster", "--name", k.clusterName, "--force"); err != nil {
		return fmt.Errorf("eksctl delete cluster: %w", err)
	}

	if k.launchTemplate != "" {
		if err := k.deleteLaunchTemplate(ctx); err != nil {
			return err
		}
	}

	// Attempt to clean up VPC if eksctl didn't fully delete it
	// This is a best-effort operation, so we only log warnings on failure
	if k.vpcId != "" {
		if err := k.cleanupVpc(ctx); err != nil {
			clog.FromContext(ctx).Warnf("VPC cleanup failed (this may be expected if eksctl already cleaned it up): %v", err)
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
	if strings.Contains(k.nodeAMI, "chainguard") {
		return "AmazonLinux2023"
	}
	return "AmazonLinux2"
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
