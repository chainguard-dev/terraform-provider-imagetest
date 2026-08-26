package ec2

import (
	"errors"
	"fmt"
	"testing"

	"github.com/aws/smithy-go"
)

func TestIsAZRetryable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "insufficient capacity",
			err: &smithy.GenericAPIError{
				Code:    "InsufficientInstanceCapacity",
				Message: "We currently do not have sufficient g6.4xlarge capacity in the Availability Zone you requested (us-west-2c).",
			},
			want: true,
		},
		{
			name: "wrapped insufficient capacity",
			err: fmt.Errorf("launching instance: %w", &smithy.GenericAPIError{
				Code: "InsufficientInstanceCapacity",
			}),
			want: true,
		},
		{
			name: "instance type not offered in zone",
			err: &smithy.GenericAPIError{
				Code:    "Unsupported",
				Message: "Your requested instance type (g6.4xlarge) is not supported in your requested Availability Zone (us-west-2d).",
			},
			want: true,
		},
		{
			name: "zone does not exist in region",
			err: &smithy.GenericAPIError{
				Code:    "InvalidParameterValue",
				Message: "Value (eu-west-2d) for parameter availabilityZone is invalid. Subnets can currently only be created in the following availability zones: eu-west-2a, eu-west-2b, eu-west-2c.",
			},
			want: true,
		},
		{
			name: "unrelated invalid parameter",
			err: &smithy.GenericAPIError{
				Code:    "InvalidParameterValue",
				Message: "Value () for parameter iamInstanceProfile.name is invalid.",
			},
			want: false,
		},
		{
			name: "unrelated unsupported",
			err: &smithy.GenericAPIError{
				Code:    "Unsupported",
				Message: "The requested configuration is currently not supported.",
			},
			want: false,
		},
		{name: "non-api error", err: errors.New("boom"), want: false},
		{name: "nil", err: nil, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isAZRetryable(tc.err); got != tc.want {
				t.Errorf("isAZRetryable(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
