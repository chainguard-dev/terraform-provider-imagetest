package ec2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/chainguard-dev/clog"
)

const (
	// ecrReadOnlyPolicyArn is the AWS managed policy for ECR read-only access.
	ecrReadOnlyPolicyArn = "arn:aws:iam::aws:policy/AmazonEC2ContainerRegistryReadOnly"

	// AWS IAM policy document values.
	iamPolicyVersion    = "2012-10-17"
	iamEffectAllow      = "Allow"
	awsServiceEC2       = "ec2.amazonaws.com"
	stsActionAssumeRole = "sts:AssumeRole"

	// IAM resource descriptions.
	iamRoleDescription = "IAM role for imagetest EC2 instance"
)

var (
	errIAMRoleCreate                = errors.New("failed to create IAM role")
	errIAMRoleAttachPolicy          = errors.New("failed to attach policy to IAM role")
	errIAMInstanceProfileCreate     = errors.New("failed to create IAM instance profile")
	errIAMInstanceProfileAddRole    = errors.New("failed to add role to instance profile")
	errIAMRoleDetachPolicy          = errors.New("failed to detach policy from IAM role")
	errIAMInstanceProfileRemoveRole = errors.New("failed to remove role from instance profile")
	errIAMInstanceProfileDelete     = errors.New("failed to delete IAM instance profile")
	errIAMRoleDelete                = errors.New("failed to delete IAM role")
	errTrustPolicyMarshal           = errors.New("failed to marshal trust policy")
)

// iamTagsDefaultWithName creates IAM tags with default tags plus a Name tag.
func iamTagsDefaultWithName(name string) []iamtypes.Tag {
	tags := []iamtypes.Tag{
		{
			Key:   aws.String(tagKeyName),
			Value: aws.String(name),
		},
		{
			Key:   aws.String(tagKeyTeam),
			Value: aws.String(tagDefaultTeam),
		},
		{
			Key:   aws.String(tagKeyProject),
			Value: aws.String(tagDefaultProject),
		},
	}
	return tags
}

// iamRoleCreate creates an IAM role with EC2 service trust policy.
func iamRoleCreate(ctx context.Context, client *iam.Client, roleName string, tags ...iamtypes.Tag) (string, error) {
	log := clog.FromContext(ctx)

	// Create the trust policy document for EC2 service.
	trustPolicy := map[string]any{
		"Version": iamPolicyVersion,
		"Statement": []map[string]any{
			{
				"Effect": iamEffectAllow,
				"Principal": map[string]any{
					"Service": awsServiceEC2,
				},
				"Action": stsActionAssumeRole,
			},
		},
	}

	trustPolicyJSON, err := json.Marshal(trustPolicy)
	if err != nil {
		return "", fmt.Errorf("%w: %w", errTrustPolicyMarshal, err)
	}

	log.Info("creating IAM role", "role_name", roleName)
	result, err := client.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 aws.String(roleName),
		AssumeRolePolicyDocument: aws.String(string(trustPolicyJSON)),
		Description:              aws.String(iamRoleDescription),
		Tags:                     tags,
	})
	if err != nil {
		return "", fmt.Errorf("%w: %w", errIAMRoleCreate, err)
	}

	log.Info("successfully created IAM role", "role_name", roleName, "role_arn", *result.Role.Arn)
	return *result.Role.Arn, nil
}

// iamRoleAttachPolicy attaches an AWS managed policy to an IAM role.
func iamRoleAttachPolicy(ctx context.Context, client *iam.Client, roleName, policyArn string) error {
	log := clog.FromContext(ctx)

	log.Info("attaching policy to IAM role", "role_name", roleName, "policy_arn", policyArn)
	_, err := client.AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{
		RoleName:  aws.String(roleName),
		PolicyArn: aws.String(policyArn),
	})
	if err != nil {
		return fmt.Errorf("%w: %w", errIAMRoleAttachPolicy, err)
	}

	log.Info("successfully attached policy to IAM role", "role_name", roleName, "policy_arn", policyArn)
	return nil
}

// iamInstanceProfileCreate creates an IAM instance profile.
func iamInstanceProfileCreate(ctx context.Context, client *iam.Client, profileName string, tags ...iamtypes.Tag) (string, error) {
	log := clog.FromContext(ctx)

	log.Info("creating IAM instance profile", "profile_name", profileName)
	result, err := client.CreateInstanceProfile(ctx, &iam.CreateInstanceProfileInput{
		InstanceProfileName: aws.String(profileName),
		Tags:                tags,
	})
	if err != nil {
		return "", fmt.Errorf("%w: %w", errIAMInstanceProfileCreate, err)
	}

	log.Info("successfully created IAM instance profile", "profile_name", profileName, "profile_arn", *result.InstanceProfile.Arn)
	return *result.InstanceProfile.Arn, nil
}

// iamInstanceProfileAddRole adds an IAM role to an instance profile.
func iamInstanceProfileAddRole(ctx context.Context, client *iam.Client, profileName, roleName string) error {
	log := clog.FromContext(ctx)

	log.Info("adding role to instance profile", "profile_name", profileName, "role_name", roleName)
	_, err := client.AddRoleToInstanceProfile(ctx, &iam.AddRoleToInstanceProfileInput{
		InstanceProfileName: aws.String(profileName),
		RoleName:            aws.String(roleName),
	})
	if err != nil {
		return fmt.Errorf("%w: %w", errIAMInstanceProfileAddRole, err)
	}

	log.Info("successfully added role to instance profile", "profile_name", profileName, "role_name", roleName)
	return nil
}

// Cleanup functions.

// iamRoleDetachPolicy detaches a policy from an IAM role.
func iamRoleDetachPolicy(ctx context.Context, client *iam.Client, roleName, policyArn string) error {
	log := clog.FromContext(ctx)

	log.Info("detaching policy from IAM role", "role_name", roleName, "policy_arn", policyArn)
	_, err := client.DetachRolePolicy(ctx, &iam.DetachRolePolicyInput{
		RoleName:  aws.String(roleName),
		PolicyArn: aws.String(policyArn),
	})
	if err != nil {
		return fmt.Errorf("%w: %w", errIAMRoleDetachPolicy, err)
	}

	log.Info("successfully detached policy from IAM role", "role_name", roleName, "policy_arn", policyArn)
	return nil
}

// iamInstanceProfileRemoveRole removes a role from an instance profile.
func iamInstanceProfileRemoveRole(ctx context.Context, client *iam.Client, profileName, roleName string) error {
	log := clog.FromContext(ctx)

	log.Info("removing role from instance profile", "profile_name", profileName, "role_name", roleName)
	_, err := client.RemoveRoleFromInstanceProfile(ctx, &iam.RemoveRoleFromInstanceProfileInput{
		InstanceProfileName: aws.String(profileName),
		RoleName:            aws.String(roleName),
	})
	if err != nil {
		return fmt.Errorf("%w: %w", errIAMInstanceProfileRemoveRole, err)
	}

	log.Info("successfully removed role from instance profile", "profile_name", profileName, "role_name", roleName)
	return nil
}

// iamInstanceProfileDelete deletes an IAM instance profile.
func iamInstanceProfileDelete(ctx context.Context, client *iam.Client, profileName string) error {
	log := clog.FromContext(ctx)

	log.Info("deleting IAM instance profile", "profile_name", profileName)
	_, err := client.DeleteInstanceProfile(ctx, &iam.DeleteInstanceProfileInput{
		InstanceProfileName: aws.String(profileName),
	})
	if err != nil {
		return fmt.Errorf("%w: %w", errIAMInstanceProfileDelete, err)
	}

	log.Info("successfully deleted IAM instance profile", "profile_name", profileName)
	return nil
}

// iamRoleDelete deletes an IAM role.
func iamRoleDelete(ctx context.Context, client *iam.Client, roleName string) error {
	log := clog.FromContext(ctx)

	log.Info("deleting IAM role", "role_name", roleName)
	_, err := client.DeleteRole(ctx, &iam.DeleteRoleInput{
		RoleName: aws.String(roleName),
	})
	if err != nil {
		return fmt.Errorf("%w: %w", errIAMRoleDelete, err)
	}

	log.Info("successfully deleted IAM role", "role_name", roleName)
	return nil
}
