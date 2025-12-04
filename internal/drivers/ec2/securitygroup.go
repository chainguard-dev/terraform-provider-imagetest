package ec2

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/chainguard-dev/clog"
)

type securityGroup struct {
	client  *ec2.Client
	vpcID   string
	name    string
	sshPort int32
	tags    []types.Tag

	id string
}

var _ resource = (*securityGroup)(nil)

func (sg *securityGroup) create(ctx context.Context) (Teardown, error) {
	log := clog.FromContext(ctx)

	result, err := sg.client.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		GroupName:   aws.String(sg.name),
		Description: aws.String("imagetest EC2 driver security group"),
		VpcId:       aws.String(sg.vpcID),
		TagSpecifications: []types.TagSpecification{{
			ResourceType: types.ResourceTypeSecurityGroup,
			Tags:         sg.tags,
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("creating security group: %w", err)
	}

	sg.id = *result.GroupId
	log.Info("created security group", "id", sg.id)

	localIP, err := publicAddr(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting local public IP: %w", err)
	}

	_, err = sg.client.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
		GroupId:    aws.String(sg.id),
		IpProtocol: aws.String("tcp"),
		FromPort:   aws.Int32(sg.sshPort),
		ToPort:     aws.Int32(sg.sshPort),
		CidrIp:     aws.String(localIP + "/32"),
	})
	if err != nil {
		return nil, fmt.Errorf("authorizing SSH ingress: %w", err)
	}
	log.Info("authorized SSH ingress", "from", localIP)

	teardown := func(ctx context.Context) error {
		log := clog.FromContext(ctx)
		log.Info("deleting security group", "id", sg.id, "name", sg.name)
		_, err := sg.client.DeleteSecurityGroup(ctx, &ec2.DeleteSecurityGroupInput{
			GroupId: aws.String(sg.id),
		})
		if err != nil {
			log.Warn("failed to delete security group", "id", sg.id, "error", err)
			return err
		}
		log.Info("security group deleted", "id", sg.id)
		return nil
	}

	return teardown, nil
}

func publicAddr(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.ipify.org", nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("looking up public IP: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode >= http.StatusBadRequest {
		return "", fmt.Errorf("looking up public IP: HTTP %d", res.StatusCode)
	}

	data, err := io.ReadAll(res.Body)
	if err != nil {
		return "", fmt.Errorf("reading public IP response: %w", err)
	}
	return string(data), nil
}
