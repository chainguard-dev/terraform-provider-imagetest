package gce

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	compute "cloud.google.com/go/compute/apiv1"
	computepb "cloud.google.com/go/compute/apiv1/computepb"
	"github.com/chainguard-dev/clog"
	"google.golang.org/protobuf/proto"
)

type instance struct {
	client    *compute.InstancesClient
	projectID string
	zone      string

	name        string
	machineType string
	image       string
	diskSizeGB  int64
	diskType    string
	network     string
	metadata    map[string]string
	tags        []string
	labels      map[string]string
	sshPort     int32

	acceleratorType  string
	acceleratorCount int32

	serviceAccountEmail string

	publicIP string
}

var _ resource = (*instance)(nil)

func (i *instance) create(ctx context.Context) (Teardown, error) {
	log := clog.FromContext(ctx)

	// Build metadata items
	metadataItems := make([]*computepb.Items, 0, len(i.metadata))
	for k, v := range i.metadata {
		metadataItems = append(metadataItems, &computepb.Items{
			Key:   proto.String(k),
			Value: proto.String(v),
		})
	}

	inst := &computepb.Instance{
		Name:        proto.String(i.name),
		MachineType: proto.String(fmt.Sprintf("zones/%s/machineTypes/%s", i.zone, i.machineType)),
		Disks: []*computepb.AttachedDisk{{
			AutoDelete: proto.Bool(true),
			Boot:       proto.Bool(true),
			InitializeParams: &computepb.AttachedDiskInitializeParams{
				SourceImage: proto.String(i.image),
				DiskSizeGb:  proto.Int64(i.diskSizeGB),
				DiskType:    proto.String(fmt.Sprintf("zones/%s/diskTypes/%s", i.zone, i.diskType)),
			},
		}},
		NetworkInterfaces: []*computepb.NetworkInterface{{
			Network: proto.String(i.network),
			AccessConfigs: []*computepb.AccessConfig{{
				Name: proto.String("External NAT"),
				Type: proto.String("ONE_TO_ONE_NAT"),
			}},
		}},
		Metadata: &computepb.Metadata{
			Items: metadataItems,
		},
		Tags: &computepb.Tags{
			Items: i.tags,
		},
		Labels: i.labels,
	}

	// Attach GPUs if requested
	if i.acceleratorCount > 0 {
		inst.GuestAccelerators = []*computepb.AcceleratorConfig{{
			AcceleratorType:  proto.String(fmt.Sprintf("zones/%s/acceleratorTypes/%s", i.zone, i.acceleratorType)),
			AcceleratorCount: proto.Int32(i.acceleratorCount),
		}}
		// GPU instances must terminate on host maintenance
		inst.Scheduling = &computepb.Scheduling{
			OnHostMaintenance: proto.String("TERMINATE"),
		}
	}

	// Set service account
	if i.serviceAccountEmail != "" {
		inst.ServiceAccounts = []*computepb.ServiceAccount{{
			Email:  proto.String(i.serviceAccountEmail),
			Scopes: []string{"https://www.googleapis.com/auth/cloud-platform"},
		}}
	} else {
		inst.ServiceAccounts = []*computepb.ServiceAccount{{
			Email:  proto.String("default"),
			Scopes: []string{"https://www.googleapis.com/auth/cloud-platform"},
		}}
	}

	op, err := i.client.Insert(ctx, &computepb.InsertInstanceRequest{
		Project:          i.projectID,
		Zone:             i.zone,
		InstanceResource: inst,
	})
	if err != nil {
		return nil, fmt.Errorf("launching instance: %w", err)
	}

	if err := op.Wait(ctx); err != nil {
		return nil, fmt.Errorf("waiting for instance creation: %w", err)
	}

	log.Info("launched instance", "name", i.name)

	teardown := func(ctx context.Context) error {
		log := clog.FromContext(ctx)
		log.Info("deleting instance", "name", i.name, "ip", i.publicIP)

		op, err := i.client.Delete(ctx, &computepb.DeleteInstanceRequest{
			Project:  i.projectID,
			Zone:     i.zone,
			Instance: i.name,
		})
		if err != nil {
			return fmt.Errorf("deleting instance: %w", err)
		}
		if err := op.Wait(ctx); err != nil {
			log.Warn("error waiting for instance deletion", "name", i.name, "error", err)
		} else {
			log.Info("instance deleted", "name", i.name)
		}
		return nil
	}

	return teardown, nil
}

func (i *instance) wait(ctx context.Context) error {
	log := clog.FromContext(ctx)

	log.Info("waiting for instance to become ready", "name", i.name)

	// Poll for RUNNING status
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			result, err := i.client.Get(ctx, &computepb.GetInstanceRequest{
				Project:  i.projectID,
				Zone:     i.zone,
				Instance: i.name,
			})
			if err != nil {
				log.Debug("instance not ready yet", "error", err)
				continue
			}

			if result.GetStatus() != "RUNNING" {
				log.Debug("instance not yet running", "status", result.GetStatus())
				continue
			}

			// Extract public IP
			if len(result.GetNetworkInterfaces()) == 0 ||
				len(result.GetNetworkInterfaces()[0].GetAccessConfigs()) == 0 {
				return fmt.Errorf("instance has no network interface or access config")
			}

			natIP := result.GetNetworkInterfaces()[0].GetAccessConfigs()[0].GetNatIP()
			if natIP == "" {
				return fmt.Errorf("instance has no external IP")
			}

			i.publicIP = natIP
			log.Info("instance ready", "name", i.name, "ip", i.publicIP)

			// Wait for SSH
			log.Info("waiting for SSH to become available", "ip", i.publicIP)
			if err := waitTCP(ctx, i.publicIP, uint16(i.sshPort)); err != nil {
				return fmt.Errorf("waiting for SSH: %w", err)
			}

			return nil
		}
	}
}

func waitTCP(ctx context.Context, host string, port uint16) error {
	log := clog.FromContext(ctx)
	target := net.JoinHostPort(host, strconv.Itoa(int(port)))
	dialer := &net.Dialer{Timeout: 3 * time.Second}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			conn, err := dialer.DialContext(ctx, "tcp", target)
			if err != nil {
				log.Debug("TCP port not ready", "target", target)
				continue
			}
			_ = conn.Close()
			log.Debug("TCP port ready", "target", target)
			return nil
		}
	}
}
