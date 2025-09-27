package ec2

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// API operation names to verify the correct IAM resource lifecycle.
const (
	opCreateRole                    = "CreateRole"
	opAttachRolePolicy              = "AttachRolePolicy"
	opCreateInstanceProfile         = "CreateInstanceProfile"
	opAddRoleToInstanceProfile      = "AddRoleToInstanceProfile"
	opRemoveRoleFromInstanceProfile = "RemoveRoleFromInstanceProfile"
	opDeleteInstanceProfile         = "DeleteInstanceProfile"
	opDetachRolePolicy              = "DetachRolePolicy"
	opDeleteRole                    = "DeleteRole"
)

// mockIAMClient is a mock implementation of the IAM client for testing.
type mockIAMClient struct {
	createRoleFunc                    func(ctx context.Context, params *iam.CreateRoleInput, optFns ...func(*iam.Options)) (*iam.CreateRoleOutput, error)
	attachRolePolicyFunc              func(ctx context.Context, params *iam.AttachRolePolicyInput, optFns ...func(*iam.Options)) (*iam.AttachRolePolicyOutput, error)
	createInstanceProfileFunc         func(ctx context.Context, params *iam.CreateInstanceProfileInput, optFns ...func(*iam.Options)) (*iam.CreateInstanceProfileOutput, error)
	addRoleToInstanceProfileFunc      func(ctx context.Context, params *iam.AddRoleToInstanceProfileInput, optFns ...func(*iam.Options)) (*iam.AddRoleToInstanceProfileOutput, error)
	removeRoleFromInstanceProfileFunc func(ctx context.Context, params *iam.RemoveRoleFromInstanceProfileInput, optFns ...func(*iam.Options)) (*iam.RemoveRoleFromInstanceProfileOutput, error)
	deleteInstanceProfileFunc         func(ctx context.Context, params *iam.DeleteInstanceProfileInput, optFns ...func(*iam.Options)) (*iam.DeleteInstanceProfileOutput, error)
	detachRolePolicyFunc              func(ctx context.Context, params *iam.DetachRolePolicyInput, optFns ...func(*iam.Options)) (*iam.DetachRolePolicyOutput, error)
	deleteRoleFunc                    func(ctx context.Context, params *iam.DeleteRoleInput, optFns ...func(*iam.Options)) (*iam.DeleteRoleOutput, error)

	// Track operations for testing.
	operations []string
}

func (m *mockIAMClient) CreateRole(ctx context.Context, params *iam.CreateRoleInput, optFns ...func(*iam.Options)) (*iam.CreateRoleOutput, error) {
	m.operations = append(m.operations, opCreateRole)
	if m.createRoleFunc != nil {
		return m.createRoleFunc(ctx, params, optFns...)
	}
	return &iam.CreateRoleOutput{
		Role: &iamtypes.Role{
			Arn:      aws.String(fmt.Sprintf("arn:aws:iam::123456789012:role/%s", *params.RoleName)),
			RoleName: params.RoleName,
		},
	}, nil
}

func (m *mockIAMClient) AttachRolePolicy(ctx context.Context, params *iam.AttachRolePolicyInput, optFns ...func(*iam.Options)) (*iam.AttachRolePolicyOutput, error) {
	m.operations = append(m.operations, opAttachRolePolicy)
	if m.attachRolePolicyFunc != nil {
		return m.attachRolePolicyFunc(ctx, params, optFns...)
	}
	return &iam.AttachRolePolicyOutput{}, nil
}

func (m *mockIAMClient) CreateInstanceProfile(ctx context.Context, params *iam.CreateInstanceProfileInput, optFns ...func(*iam.Options)) (*iam.CreateInstanceProfileOutput, error) {
	m.operations = append(m.operations, opCreateInstanceProfile)
	if m.createInstanceProfileFunc != nil {
		return m.createInstanceProfileFunc(ctx, params, optFns...)
	}
	return &iam.CreateInstanceProfileOutput{
		InstanceProfile: &iamtypes.InstanceProfile{
			Arn:                 aws.String(fmt.Sprintf("arn:aws:iam::123456789012:instance-profile/%s", *params.InstanceProfileName)),
			InstanceProfileName: params.InstanceProfileName,
		},
	}, nil
}

func (m *mockIAMClient) AddRoleToInstanceProfile(ctx context.Context, params *iam.AddRoleToInstanceProfileInput, optFns ...func(*iam.Options)) (*iam.AddRoleToInstanceProfileOutput, error) {
	m.operations = append(m.operations, opAddRoleToInstanceProfile)
	if m.addRoleToInstanceProfileFunc != nil {
		return m.addRoleToInstanceProfileFunc(ctx, params, optFns...)
	}
	return &iam.AddRoleToInstanceProfileOutput{}, nil
}

func (m *mockIAMClient) RemoveRoleFromInstanceProfile(ctx context.Context, params *iam.RemoveRoleFromInstanceProfileInput, optFns ...func(*iam.Options)) (*iam.RemoveRoleFromInstanceProfileOutput, error) {
	m.operations = append(m.operations, opRemoveRoleFromInstanceProfile)
	if m.removeRoleFromInstanceProfileFunc != nil {
		return m.removeRoleFromInstanceProfileFunc(ctx, params, optFns...)
	}
	return &iam.RemoveRoleFromInstanceProfileOutput{}, nil
}

func (m *mockIAMClient) DeleteInstanceProfile(ctx context.Context, params *iam.DeleteInstanceProfileInput, optFns ...func(*iam.Options)) (*iam.DeleteInstanceProfileOutput, error) {
	m.operations = append(m.operations, opDeleteInstanceProfile)
	if m.deleteInstanceProfileFunc != nil {
		return m.deleteInstanceProfileFunc(ctx, params, optFns...)
	}
	return &iam.DeleteInstanceProfileOutput{}, nil
}

func (m *mockIAMClient) DetachRolePolicy(ctx context.Context, params *iam.DetachRolePolicyInput, optFns ...func(*iam.Options)) (*iam.DetachRolePolicyOutput, error) {
	m.operations = append(m.operations, opDetachRolePolicy)
	if m.detachRolePolicyFunc != nil {
		return m.detachRolePolicyFunc(ctx, params, optFns...)
	}
	return &iam.DetachRolePolicyOutput{}, nil
}

func (m *mockIAMClient) DeleteRole(ctx context.Context, params *iam.DeleteRoleInput, optFns ...func(*iam.Options)) (*iam.DeleteRoleOutput, error) {
	m.operations = append(m.operations, opDeleteRole)
	if m.deleteRoleFunc != nil {
		return m.deleteRoleFunc(ctx, params, optFns...)
	}
	return &iam.DeleteRoleOutput{}, nil
}

func TestIamTagsWithDefaults(t *testing.T) {
	testName := "test-role"
	tags := iamTagsDefaultWithName(testName)

	require.Len(t, tags, 3, "should have exactly 3 tags")

	// Check Name tag.
	nameTag := findTag(tags, tagKeyName)
	require.NotNil(t, nameTag, "should have Name tag")
	assert.Equal(t, testName, *nameTag.Value)

	// Check Team tag.
	teamTag := findTag(tags, tagKeyTeam)
	require.NotNil(t, teamTag, "should have Team tag")
	assert.Equal(t, tagDefaultTeam, *teamTag.Value)

	// Check Project tag.
	projectTag := findTag(tags, tagKeyProject)
	require.NotNil(t, projectTag, "should have Project tag")
	assert.Equal(t, tagDefaultProject, *projectTag.Value)
}

func TestIamRoleCreate(t *testing.T) {
	tests := []struct {
		name          string
		roleName      string
		mockSetup     func(*mockIAMClient)
		expectedError error
		expectedArn   string
		validateInput func(t *testing.T, input *iam.CreateRoleInput)
	}{
		{
			name:     "successful role creation",
			roleName: "test-role",
			mockSetup: func(m *mockIAMClient) {
				// Use default mock behavior (returns success).
			},
			expectedArn: "arn:aws:iam::123456789012:role/test-role",
		},
		{
			name:     "role creation with correct parameters",
			roleName: "test-role-params",
			mockSetup: func(m *mockIAMClient) {
				// Mock will capture input for validation.
			},
			expectedArn: "arn:aws:iam::123456789012:role/test-role-params",
			validateInput: func(t *testing.T, input *iam.CreateRoleInput) {
				assert.Equal(t, "test-role-params", *input.RoleName)
				assert.Contains(t, *input.AssumeRolePolicyDocument, "ec2.amazonaws.com")
				assert.Contains(t, *input.AssumeRolePolicyDocument, "sts:AssumeRole")
				assert.Len(t, input.Tags, 3)
			},
		},
		{
			name:     "role creation failure",
			roleName: "failing-role",
			mockSetup: func(m *mockIAMClient) {
				m.createRoleFunc = func(ctx context.Context, params *iam.CreateRoleInput, optFns ...func(*iam.Options)) (*iam.CreateRoleOutput, error) {
					return nil, fmt.Errorf("AWS quota exceeded")
				}
			},
			expectedError: errIAMRoleCreate,
		},
		{
			name:     "role creation with empty name",
			roleName: "",
			mockSetup: func(m *mockIAMClient) {
				m.createRoleFunc = func(ctx context.Context, params *iam.CreateRoleInput, optFns ...func(*iam.Options)) (*iam.CreateRoleOutput, error) {
					return nil, fmt.Errorf("ValidationException: Role name cannot be empty")
				}
			},
			expectedError: errIAMRoleCreate,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			mockClient := &mockIAMClient{}
			var capturedInput *iam.CreateRoleInput

			// Setup input capture if validation is needed.
			if tt.validateInput != nil {
				mockClient.createRoleFunc = func(ctx context.Context, params *iam.CreateRoleInput, optFns ...func(*iam.Options)) (*iam.CreateRoleOutput, error) {
					capturedInput = params
					return &iam.CreateRoleOutput{
						Role: &iamtypes.Role{
							Arn:      aws.String("arn:aws:iam::123456789012:role/" + *params.RoleName),
							RoleName: params.RoleName,
						},
					}, nil
				}
			}

			tt.mockSetup(mockClient)

			arn, err := iamRoleCreate(ctx, mockClient, tt.roleName, iamTagsDefaultWithName(tt.roleName)...)

			if tt.expectedError != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.expectedError)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedArn, arn)

				if tt.validateInput != nil {
					require.NotNil(t, capturedInput, "input should be captured for validation")
					tt.validateInput(t, capturedInput)
				}
			}
		})
	}
}

func TestIamRoleAttachPolicy(t *testing.T) {
	tests := []struct {
		name          string
		roleName      string
		policyArn     string
		mockSetup     func(*mockIAMClient)
		expectedError string
		validateInput func(t *testing.T, input *iam.AttachRolePolicyInput)
	}{
		{
			name:      "successful policy attachment",
			roleName:  "test-role",
			policyArn: ecrReadOnlyPolicyArn,
			mockSetup: func(m *mockIAMClient) {
				// Use default mock behavior (returns success).
			},
		},
		{
			name:      "policy attachment with correct parameters",
			roleName:  "param-test-role",
			policyArn: ecrReadOnlyPolicyArn,
			mockSetup: func(m *mockIAMClient) {
				// Mock will capture input for validation.
			},
			validateInput: func(t *testing.T, input *iam.AttachRolePolicyInput) {
				assert.Equal(t, "param-test-role", *input.RoleName)
				assert.Equal(t, ecrReadOnlyPolicyArn, *input.PolicyArn)
			},
		},
		{
			name:      "policy attachment failure",
			roleName:  "failing-role",
			policyArn: ecrReadOnlyPolicyArn,
			mockSetup: func(m *mockIAMClient) {
				m.attachRolePolicyFunc = func(ctx context.Context, params *iam.AttachRolePolicyInput, optFns ...func(*iam.Options)) (*iam.AttachRolePolicyOutput, error) {
					return nil, fmt.Errorf("AWS access denied")
				}
			},
			expectedError: "failed to attach policy to IAM role",
		},
		{
			name:      "invalid policy ARN",
			roleName:  "test-role",
			policyArn: "invalid-arn",
			mockSetup: func(m *mockIAMClient) {
				m.attachRolePolicyFunc = func(ctx context.Context, params *iam.AttachRolePolicyInput, optFns ...func(*iam.Options)) (*iam.AttachRolePolicyOutput, error) {
					return nil, fmt.Errorf("MalformedPolicyDocument: Invalid policy ARN")
				}
			},
			expectedError: "failed to attach policy to IAM role",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			mockClient := &mockIAMClient{}
			var capturedInput *iam.AttachRolePolicyInput

			// Setup input capture if validation is needed.
			if tt.validateInput != nil {
				mockClient.attachRolePolicyFunc = func(ctx context.Context, params *iam.AttachRolePolicyInput, optFns ...func(*iam.Options)) (*iam.AttachRolePolicyOutput, error) {
					capturedInput = params
					return &iam.AttachRolePolicyOutput{}, nil
				}
			}

			tt.mockSetup(mockClient)

			err := iamRoleAttachPolicy(ctx, mockClient, tt.roleName, tt.policyArn)

			if tt.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			} else {
				require.NoError(t, err)

				if tt.validateInput != nil {
					require.NotNil(t, capturedInput, "input should be captured for validation")
					tt.validateInput(t, capturedInput)
				}
			}
		})
	}
}

func TestIamInstanceProfileCreate(t *testing.T) {
	ctx := context.Background()
	profileName := "test-profile"
	mockClient := &mockIAMClient{}

	t.Run("successful instance profile creation", func(t *testing.T) {
		arn, err := iamInstanceProfileCreate(ctx, mockClient, profileName, iamTagsDefaultWithName(profileName)...)
		require.NoError(t, err)
		assert.Equal(t, "arn:aws:iam::123456789012:instance-profile/test-profile", arn)
	})

	t.Run("instance profile creation with correct parameters", func(t *testing.T) {
		var capturedInput *iam.CreateInstanceProfileInput
		mockClient.createInstanceProfileFunc = func(ctx context.Context, params *iam.CreateInstanceProfileInput, optFns ...func(*iam.Options)) (*iam.CreateInstanceProfileOutput, error) {
			capturedInput = params
			return &iam.CreateInstanceProfileOutput{
				InstanceProfile: &iamtypes.InstanceProfile{
					Arn:                 aws.String("arn:aws:iam::123456789012:instance-profile/test-profile"),
					InstanceProfileName: params.InstanceProfileName,
				},
			}, nil
		}

		_, err := iamInstanceProfileCreate(ctx, mockClient, profileName, iamTagsDefaultWithName(profileName)...)
		require.NoError(t, err)
		require.NotNil(t, capturedInput)

		assert.Equal(t, profileName, *capturedInput.InstanceProfileName)
		assert.Len(t, capturedInput.Tags, 3)
	})

	t.Run("instance profile creation failure", func(t *testing.T) {
		expectedError := fmt.Errorf("AWS error")
		mockClient.createInstanceProfileFunc = func(ctx context.Context, params *iam.CreateInstanceProfileInput, optFns ...func(*iam.Options)) (*iam.CreateInstanceProfileOutput, error) {
			return nil, expectedError
		}

		_, err := iamInstanceProfileCreate(ctx, mockClient, profileName, iamTagsDefaultWithName(profileName)...)
		require.Error(t, err)
		assert.ErrorIs(t, err, errIAMInstanceProfileCreate)
	})
}

func TestIamInstanceProfileAddRole(t *testing.T) {
	ctx := context.Background()
	profileName := "test-profile"
	roleName := "test-role"
	mockClient := &mockIAMClient{}

	t.Run("successful role addition to instance profile", func(t *testing.T) {
		err := iamInstanceProfileAddRole(ctx, mockClient, profileName, roleName)
		require.NoError(t, err)
	})

	t.Run("role addition with correct parameters", func(t *testing.T) {
		var capturedInput *iam.AddRoleToInstanceProfileInput
		mockClient.addRoleToInstanceProfileFunc = func(ctx context.Context, params *iam.AddRoleToInstanceProfileInput, optFns ...func(*iam.Options)) (*iam.AddRoleToInstanceProfileOutput, error) {
			capturedInput = params
			return &iam.AddRoleToInstanceProfileOutput{}, nil
		}

		err := iamInstanceProfileAddRole(ctx, mockClient, profileName, roleName)
		require.NoError(t, err)
		require.NotNil(t, capturedInput)

		assert.Equal(t, profileName, *capturedInput.InstanceProfileName)
		assert.Equal(t, roleName, *capturedInput.RoleName)
	})

	t.Run("role addition failure", func(t *testing.T) {
		expectedError := fmt.Errorf("AWS error")
		mockClient.addRoleToInstanceProfileFunc = func(ctx context.Context, params *iam.AddRoleToInstanceProfileInput, optFns ...func(*iam.Options)) (*iam.AddRoleToInstanceProfileOutput, error) {
			return nil, expectedError
		}

		err := iamInstanceProfileAddRole(ctx, mockClient, profileName, roleName)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to add role to instance profile")
	})
}

func TestIamCleanupSequence(t *testing.T) {
	ctx := context.Background()
	profileName := "test-profile"
	roleName := "test-role"
	mockClient := &mockIAMClient{}

	t.Run("cleanup operations execute in correct order", func(t *testing.T) {
		// Test each cleanup function individually - the mock client automatically tracks operations.
		err := iamInstanceProfileRemoveRole(ctx, mockClient, profileName, roleName)
		require.NoError(t, err)

		err = iamInstanceProfileDelete(ctx, mockClient, profileName)
		require.NoError(t, err)

		err = iamRoleDetachPolicy(ctx, mockClient, roleName, ecrReadOnlyPolicyArn)
		require.NoError(t, err)

		err = iamRoleDelete(ctx, mockClient, roleName)
		require.NoError(t, err)

		// Verify the operations were called in the correct order using built-in tracking.
		expected := []string{
			opRemoveRoleFromInstanceProfile,
			opDeleteInstanceProfile,
			opDetachRolePolicy,
			opDeleteRole,
		}
		assert.Equal(t, expected, mockClient.operations)
	})
}

func TestEnsureInstanceProfileWorkflow(t *testing.T) {
	tests := []struct {
		name               string
		inputProfile       string
		runID              string
		mockSetup          func(*mockIAMClient) []string // Returns operation order.
		expectedResult     string
		expectedError      string
		expectedOperations []string
	}{
		{
			name:         "uses existing profile when provided",
			inputProfile: "existing-profile",
			runID:        "test-123",
			mockSetup: func(m *mockIAMClient) []string {
				// No operations should be performed.
				return []string{}
			},
			expectedResult: "existing-profile",
		},
		{
			name:         "creates new profile when none provided",
			inputProfile: "",
			runID:        "test-123",
			mockSetup: func(m *mockIAMClient) []string {
				m.createRoleFunc = func(ctx context.Context, params *iam.CreateRoleInput, optFns ...func(*iam.Options)) (*iam.CreateRoleOutput, error) {
					return &iam.CreateRoleOutput{
						Role: &iamtypes.Role{
							Arn:      aws.String("arn:aws:iam::123456789012:role/" + *params.RoleName),
							RoleName: params.RoleName,
						},
					}, nil
				}

				m.attachRolePolicyFunc = func(ctx context.Context, params *iam.AttachRolePolicyInput, optFns ...func(*iam.Options)) (*iam.AttachRolePolicyOutput, error) {
					return &iam.AttachRolePolicyOutput{}, nil
				}

				m.createInstanceProfileFunc = func(ctx context.Context, params *iam.CreateInstanceProfileInput, optFns ...func(*iam.Options)) (*iam.CreateInstanceProfileOutput, error) {
					return &iam.CreateInstanceProfileOutput{
						InstanceProfile: &iamtypes.InstanceProfile{
							Arn:                 aws.String("arn:aws:iam::123456789012:instance-profile/" + *params.InstanceProfileName),
							InstanceProfileName: params.InstanceProfileName,
						},
					}, nil
				}

				m.addRoleToInstanceProfileFunc = func(ctx context.Context, params *iam.AddRoleToInstanceProfileInput, optFns ...func(*iam.Options)) (*iam.AddRoleToInstanceProfileOutput, error) {
					return &iam.AddRoleToInstanceProfileOutput{}, nil
				}

				return []string{opCreateRole, opAttachRolePolicy, opCreateInstanceProfile, opAddRoleToInstanceProfile}
			},
			expectedResult:     "test-123-profile",
			expectedOperations: []string{opCreateRole, opAttachRolePolicy, opCreateInstanceProfile, opAddRoleToInstanceProfile},
		},
		{
			name:         "handles role creation failure gracefully",
			inputProfile: "",
			runID:        "test-456",
			mockSetup: func(m *mockIAMClient) []string {
				m.createRoleFunc = func(ctx context.Context, params *iam.CreateRoleInput, optFns ...func(*iam.Options)) (*iam.CreateRoleOutput, error) {
					return nil, fmt.Errorf("AWS IAM quota exceeded")
				}
				return []string{}
			},
			expectedError: "failed to create IAM role",
		},
		{
			name:         "handles policy attachment failure",
			inputProfile: "",
			runID:        "test-789",
			mockSetup: func(m *mockIAMClient) []string {
				m.createRoleFunc = func(ctx context.Context, params *iam.CreateRoleInput, optFns ...func(*iam.Options)) (*iam.CreateRoleOutput, error) {
					return &iam.CreateRoleOutput{
						Role: &iamtypes.Role{
							Arn:      aws.String("arn:aws:iam::123456789012:role/" + *params.RoleName),
							RoleName: params.RoleName,
						},
					}, nil
				}
				m.attachRolePolicyFunc = func(ctx context.Context, params *iam.AttachRolePolicyInput, optFns ...func(*iam.Options)) (*iam.AttachRolePolicyOutput, error) {
					return nil, fmt.Errorf("AccessDenied: Cannot attach policy")
				}
				return []string{}
			},
			expectedError: "failed to attach policy to IAM role",
		},
		{
			name:         "handles instance profile creation failure",
			inputProfile: "",
			runID:        "test-999",
			mockSetup: func(m *mockIAMClient) []string {
				m.createRoleFunc = func(ctx context.Context, params *iam.CreateRoleInput, optFns ...func(*iam.Options)) (*iam.CreateRoleOutput, error) {
					return &iam.CreateRoleOutput{
						Role: &iamtypes.Role{
							Arn:      aws.String("arn:aws:iam::123456789012:role/" + *params.RoleName),
							RoleName: params.RoleName,
						},
					}, nil
				}
				m.attachRolePolicyFunc = func(ctx context.Context, params *iam.AttachRolePolicyInput, optFns ...func(*iam.Options)) (*iam.AttachRolePolicyOutput, error) {
					return &iam.AttachRolePolicyOutput{}, nil
				}
				m.createInstanceProfileFunc = func(ctx context.Context, params *iam.CreateInstanceProfileInput, optFns ...func(*iam.Options)) (*iam.CreateInstanceProfileOutput, error) {
					return nil, fmt.Errorf("EntityAlreadyExistsException: Instance profile already exists")
				}
				return []string{}
			},
			expectedError: "failed to create instance profile",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			mockClient := &mockIAMClient{}
			tt.mockSetup(mockClient)

			driver := &Driver{
				runID:     tt.runID,
				iamClient: mockClient,
				stack:     stack{},
			}

			result, err := driver.ensureInstanceProfile(ctx, tt.inputProfile)

			if tt.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedResult, result)

				if tt.expectedOperations != nil {
					assert.Equal(t, tt.expectedOperations, mockClient.operations, "operations should occur in correct order")
				}
			}
		})
	}
}

// Helper function to find a tag by key.
func findTag(tags []iamtypes.Tag, key string) *iamtypes.Tag {
	for i, tag := range tags {
		if tag.Key != nil && *tag.Key == key {
			return &tags[i]
		}
	}
	return nil
}
