//go:build floci

package ec2

// Integration test against floci (https://github.com/hectorvent/floci), a
// LocalStack-wire-compatible AWS emulator whose EC2 implementation backs
// RunInstances with real containers. It exercises the placement path the AZ
// failover change touches - security group, key pair, AZ-bound subnet
// placement, instance launch, and stack teardown - through the driver's real
// code, without AWS credentials. It does not run the full Setup (no IAM
// instance profile, no instance wait/SSH) and it cannot exercise the
// InsufficientInstanceCapacity failover branch itself: emulators never run
// out of capacity, so that classification stays unit-tested in
// instance_test.go.
//
// Run with:
//
//	docker run -d --name floci -p 4566:4566 \
//	  -v /var/run/docker.sock:/var/run/docker.sock -u root floci/floci:latest
//	FLOCI_ENDPOINT=http://localhost:4566 go test -tags floci -count 1 \
//	  -run TestFlociPlacement ./internal/drivers/ec2/
//
// The security group's caller-IP discovery (api.ipify.org) is stubbed out so
// the test runs in egress-restricted environments.

import (
	"context"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
)

func flociClients(t *testing.T) (*ec2.Client, *iam.Client) {
	t.Helper()
	endpoint := os.Getenv("FLOCI_ENDPOINT")
	if endpoint == "" {
		// The build tag is already an explicit opt-in; a missing endpoint is
		// a misconfiguration, not a reason to silently report green.
		t.Fatal("FLOCI_ENDPOINT must be set (e.g. http://localhost:4566)")
	}

	cfg := aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("test", "test", ""),
	}
	ec2Client := ec2.NewFromConfig(cfg, func(o *ec2.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})
	iamClient := iam.NewFromConfig(cfg, func(o *iam.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})
	return ec2Client, iamClient
}

// stubIpify makes securityGroup.create's caller-IP lookup hermetic by
// answering api.ipify.org requests locally. Test must not be parallel.
type stubIpify struct{ next http.RoundTripper }

func (s *stubIpify) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host == "api.ipify.org" {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("203.0.113.10")),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	}
	next := s.next
	if next == nil {
		next = http.DefaultTransport
	}
	return next.RoundTrip(req)
}

func TestFlociPlacement(t *testing.T) {
	ec2Client, iamClient := flociClients(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	prevTransport := http.DefaultClient.Transport
	http.DefaultClient.Transport = &stubIpify{next: prevTransport}
	t.Cleanup(func() { http.DefaultClient.Transport = prevTransport })

	// Pre-provision the VPC the driver expects.
	vpc, err := ec2Client.CreateVpc(ctx, &ec2.CreateVpcInput{
		CidrBlock: aws.String("10.42.0.0/16"),
	})
	if err != nil {
		t.Fatalf("creating VPC: %v", err)
	}
	vpcID := *vpc.Vpc.VpcId
	t.Cleanup(func() {
		if _, err := ec2Client.DeleteVpc(context.Background(), &ec2.DeleteVpcInput{VpcId: aws.String(vpcID)}); err != nil {
			t.Logf("deleting VPC: %v", err)
		}
	})

	d, err := NewDriver(Config{
		VPCID:  vpcID,
		AMI:    "ami-floci-test",
		Region: "us-east-1",
	}, ec2Client, iamClient)
	if err != nil {
		t.Fatalf("NewDriver: %v", err)
	}
	d.name = "imagetest-floci"

	// Safety net: tear the stack down even if an assertion Fatals mid-test.
	torndown := false
	t.Cleanup(func() {
		if !torndown {
			if err := d.Teardown(context.Background()); err != nil {
				t.Logf("cleanup teardown: %v", err)
			}
		}
	})

	// The pieces of setupNewInstance that placement depends on.
	d.sg = &securityGroup{
		client:  d.ec2,
		vpcID:   vpcID,
		name:    d.name + "-sg",
		sshPort: d.cfg.SSHPort,
		tags:    d.buildTags(d.name + "-sg"),
	}
	if err := d.create(ctx, d.sg); err != nil {
		t.Fatalf("creating security group: %v", err)
	}
	d.key = &keyPair{
		client: d.ec2,
		name:   d.name + "-key",
		tags:   d.buildTags(d.name + "-key"),
	}
	if err := d.create(ctx, d.key); err != nil {
		t.Fatalf("creating key pair: %v", err)
	}

	// The changed surface: AZ-bound subnet + instance placement.
	if err := d.createPlacement(ctx, ""); err != nil {
		t.Fatalf("createPlacement: %v", err)
	}
	if d.instance == nil || d.instance.id == "" {
		t.Fatal("createPlacement returned without a launched instance")
	}
	if d.subnet == nil || d.subnet.id == "" {
		t.Fatal("createPlacement returned without a placed subnet")
	}

	// The chosen AZ must be one of the region's candidates.
	wantAZs := make([]string, len(azSuffixes))
	for i, s := range azSuffixes {
		wantAZs[i] = d.cfg.Region + s
	}
	if !slices.Contains(wantAZs, d.subnet.az) {
		t.Errorf("placed AZ %s not in candidates %v", d.subnet.az, wantAZs)
	}

	// The subnet must exist, in the AZ the placement chose.
	subnets, err := ec2Client.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
		SubnetIds: []string{d.subnet.id},
	})
	if err != nil || len(subnets.Subnets) != 1 {
		t.Fatalf("describing placed subnet %s: %v (%d found)", d.subnet.id, err, len(subnets.Subnets))
	}
	if got := aws.ToString(subnets.Subnets[0].AvailabilityZone); got != d.subnet.az {
		t.Errorf("subnet AZ = %s, want %s", got, d.subnet.az)
	}

	// The instance must exist, in the placed subnet (and its placement AZ
	// must agree, when the emulator reports one).
	instances, err := ec2Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{d.instance.id},
	})
	if err != nil || len(instances.Reservations) != 1 || len(instances.Reservations[0].Instances) != 1 {
		t.Fatalf("describing instance %s: %v", d.instance.id, err)
	}
	inst := instances.Reservations[0].Instances[0]
	if got := aws.ToString(inst.SubnetId); got != d.subnet.id {
		t.Errorf("instance subnet = %s, want %s", got, d.subnet.id)
	}
	if inst.Placement != nil && inst.Placement.AvailabilityZone != nil {
		if got := *inst.Placement.AvailabilityZone; got != d.subnet.az {
			t.Errorf("instance placement AZ = %s, want %s", got, d.subnet.az)
		}
	}
	if inst.State.Name == types.InstanceStateNameTerminated {
		t.Errorf("instance unexpectedly terminated after launch")
	}

	// Stack teardown. (Deletion order is instance-then-subnet by
	// construction; an emulator won't necessarily enforce the dependency, so
	// only absence is asserted here.)
	if err := d.Teardown(ctx); err != nil {
		t.Fatalf("Teardown: %v", err)
	}
	torndown = true

	// Subnet gone: either a NotFound error or an empty successful response.
	after, err := ec2Client.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
		SubnetIds: []string{d.subnet.id},
	})
	switch {
	case err == nil && len(after.Subnets) > 0:
		t.Errorf("subnet %s still exists after teardown", d.subnet.id)
	case err != nil && !strings.Contains(err.Error(), "NotFound"):
		t.Errorf("unexpected error describing subnet after teardown: %v", err)
	}

	// Instance terminated (terminated instances remain describable).
	afterInst, err := ec2Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{d.instance.id},
	})
	if err != nil {
		if !strings.Contains(err.Error(), "NotFound") {
			t.Errorf("unexpected error describing instance after teardown: %v", err)
		}
		return
	}
	for _, r := range afterInst.Reservations {
		for _, i := range r.Instances {
			if i.State.Name != types.InstanceStateNameTerminated {
				t.Errorf("instance %s in state %s after teardown, want terminated", d.instance.id, i.State.Name)
			}
		}
	}
}
