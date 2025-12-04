package ec2

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/chainguard-dev/clog"
)

const ecrReadOnlyPolicyArn = "arn:aws:iam::aws:policy/AmazonEC2ContainerRegistryReadOnly"

type instanceProfile struct {
	client     *iam.Client
	namePrefix string
	tags       []iamtypes.Tag

	roleName    string
	profileName string
}

var _ resource = (*instanceProfile)(nil)

func (p *instanceProfile) create(ctx context.Context) (Teardown, error) {
	log := clog.FromContext(ctx)

	p.roleName = p.namePrefix + "-role"
	p.profileName = p.namePrefix + "-profile"

	trustPolicy := map[string]any{
		"Version": "2012-10-17",
		"Statement": []map[string]any{{
			"Effect":    "Allow",
			"Principal": map[string]any{"Service": "ec2.amazonaws.com"},
			"Action":    "sts:AssumeRole",
		}},
	}
	trustPolicyJSON, err := json.Marshal(trustPolicy)
	if err != nil {
		return nil, fmt.Errorf("marshaling trust policy: %w", err)
	}

	_, err = p.client.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 aws.String(p.roleName),
		AssumeRolePolicyDocument: aws.String(string(trustPolicyJSON)),
		Description:              aws.String("imagetest EC2 driver IAM role"),
		Tags:                     p.tags,
	})
	if err != nil {
		return nil, fmt.Errorf("creating IAM role: %w", err)
	}
	log.Info("created IAM role", "name", p.roleName)

	_, err = p.client.AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{
		RoleName:  aws.String(p.roleName),
		PolicyArn: aws.String(ecrReadOnlyPolicyArn),
	})
	if err != nil {
		return nil, fmt.Errorf("attaching policy to role: %w", err)
	}
	log.Info("attached ECR read-only policy", "role", p.roleName)

	_, err = p.client.CreateInstanceProfile(ctx, &iam.CreateInstanceProfileInput{
		InstanceProfileName: aws.String(p.profileName),
		Tags:                p.tags,
	})
	if err != nil {
		return nil, fmt.Errorf("creating instance profile: %w", err)
	}
	log.Info("created instance profile", "name", p.profileName)

	_, err = p.client.AddRoleToInstanceProfile(ctx, &iam.AddRoleToInstanceProfileInput{
		InstanceProfileName: aws.String(p.profileName),
		RoleName:            aws.String(p.roleName),
	})
	if err != nil {
		return nil, fmt.Errorf("adding role to instance profile: %w", err)
	}
	log.Info("added role to instance profile", "role", p.roleName, "profile", p.profileName)

	teardown := func(ctx context.Context) error {
		log := clog.FromContext(ctx)

		log.Info("removing role from instance profile", "role", p.roleName, "profile", p.profileName)
		_, err := p.client.RemoveRoleFromInstanceProfile(ctx, &iam.RemoveRoleFromInstanceProfileInput{
			InstanceProfileName: aws.String(p.profileName),
			RoleName:            aws.String(p.roleName),
		})
		if err != nil {
			return fmt.Errorf("removing role from instance profile: %w", err)
		}

		log.Info("deleting instance profile", "name", p.profileName)
		_, err = p.client.DeleteInstanceProfile(ctx, &iam.DeleteInstanceProfileInput{
			InstanceProfileName: aws.String(p.profileName),
		})
		if err != nil {
			return fmt.Errorf("deleting instance profile: %w", err)
		}

		log.Info("detaching policy from role", "role", p.roleName)
		_, err = p.client.DetachRolePolicy(ctx, &iam.DetachRolePolicyInput{
			RoleName:  aws.String(p.roleName),
			PolicyArn: aws.String(ecrReadOnlyPolicyArn),
		})
		if err != nil {
			return fmt.Errorf("detaching policy from role: %w", err)
		}

		log.Info("deleting IAM role", "name", p.roleName)
		_, err = p.client.DeleteRole(ctx, &iam.DeleteRoleInput{
			RoleName: aws.String(p.roleName),
		})
		if err != nil {
			return fmt.Errorf("deleting IAM role: %w", err)
		}

		return nil
	}

	return teardown, nil
}
