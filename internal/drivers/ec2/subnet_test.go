package ec2

import (
	"net"
	"testing"
)

func TestRandomCIDR(t *testing.T) {
	tests := []struct {
		name       string
		vpcCIDR    string
		numSubnets int
	}{
		{
			name:       "/16 VPC at 10.0.0.0",
			vpcCIDR:    "10.0.0.0/16",
			numSubnets: 4096,
		},
		{
			name:       "/16 VPC at 10.1.0.0",
			vpcCIDR:    "10.1.0.0/16",
			numSubnets: 4096,
		},
		{
			name:       "/20 VPC at 172.16.32.0",
			vpcCIDR:    "172.16.32.0/20",
			numSubnets: 256,
		},
		{
			name:       "/24 VPC",
			vpcCIDR:    "192.168.1.0/24",
			numSubnets: 16,
		},
		{
			name:       "/28 VPC (minimum)",
			vpcCIDR:    "192.168.1.0/28",
			numSubnets: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, vpcNet, err := net.ParseCIDR(tc.vpcCIDR)
			if err != nil {
				t.Fatalf("invalid test CIDR %s: %v", tc.vpcCIDR, err)
			}

			for range 100 {
				cidr := randomCIDR(vpcNet, tc.numSubnets)

				subnetIP, subnetNet, err := net.ParseCIDR(cidr)
				if err != nil {
					t.Errorf("randomCIDR returned invalid CIDR %q: %v", cidr, err)
					continue
				}

				ones, _ := subnetNet.Mask.Size()
				if ones != 28 {
					t.Errorf("expected /28, got /%d for %s", ones, cidr)
				}

				if !vpcNet.Contains(subnetIP) {
					t.Errorf("subnet %s not contained in VPC %s", cidr, tc.vpcCIDR)
				}
			}
		})
	}
}
