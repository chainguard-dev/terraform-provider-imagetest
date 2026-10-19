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

// TestInstanceAddr checks that the driver connects by public DNS name when
// the VPC assigns one and falls back to the public IP when it does not.
func TestInstanceAddr(t *testing.T) {
	tests := []struct {
		name string
		inst instance
		want string
	}{
		{
			name: "DNS name assigned",
			inst: instance{publicIP: "203.0.113.10", publicDNS: "ec2-203-0-113-10.us-west-2.compute.amazonaws.com"},
			want: "ec2-203-0-113-10.us-west-2.compute.amazonaws.com",
		},
		{
			name: "no DNS name",
			inst: instance{publicIP: "203.0.113.10"},
			want: "203.0.113.10",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.inst.addr(); got != tc.want {
				t.Errorf("addr() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestResolvesTo checks that a name only counts as leading to an IP when it
// resolves to that IP, so the driver falls back to the IP where the name
// resolves elsewhere (such as inside the instance's VPC) or not at all.
func TestResolvesTo(t *testing.T) {
	tests := []struct {
		name string
		host string
		ip   string
		want bool
	}{
		{name: "resolves to the IP", host: "localhost", ip: "127.0.0.1", want: true},
		{name: "resolves elsewhere", host: "localhost", ip: "203.0.113.10", want: false},
		{name: "does not resolve", host: "imagetest.invalid", ip: "203.0.113.10", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolvesTo(t.Context(), tc.host, tc.ip); got != tc.want {
				t.Errorf("resolvesTo(%q, %q) = %v, want %v", tc.host, tc.ip, got, tc.want)
			}
		})
	}
}
