package gce

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	compute "cloud.google.com/go/compute/apiv1"
	computepb "cloud.google.com/go/compute/apiv1/computepb"
	"github.com/chainguard-dev/clog"
	"google.golang.org/protobuf/proto"
)

type firewallRule struct {
	client    *compute.FirewallsClient
	projectID string
	network   string
	name      string
	sshPort   int32
	tag       string
}

var _ resource = (*firewallRule)(nil)

func (fw *firewallRule) create(ctx context.Context) (Teardown, error) {
	log := clog.FromContext(ctx)

	localIP, err := publicAddr(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting local public IP: %w", err)
	}

	rule := &computepb.Firewall{
		Name:         proto.String(fw.name),
		Network:      proto.String(fw.network),
		Description:  proto.String("imagetest GCE driver firewall rule"),
		SourceRanges: []string{localIP + "/32"},
		TargetTags:   []string{fw.tag},
		Allowed: []*computepb.Allowed{{
			IPProtocol: proto.String("tcp"),
			Ports:      []string{fmt.Sprintf("%d", fw.sshPort)},
		}},
	}

	op, err := fw.client.Insert(ctx, &computepb.InsertFirewallRequest{
		Project:          fw.projectID,
		FirewallResource: rule,
	})
	if err != nil {
		return nil, fmt.Errorf("creating firewall rule: %w", err)
	}

	if err := op.Wait(ctx); err != nil {
		return nil, fmt.Errorf("waiting for firewall rule creation: %w", err)
	}

	log.Info("created firewall rule", "name", fw.name, "source", localIP)

	teardown := func(ctx context.Context) error {
		log := clog.FromContext(ctx)
		log.Info("deleting firewall rule", "name", fw.name)
		op, err := fw.client.Delete(ctx, &computepb.DeleteFirewallRequest{
			Project:  fw.projectID,
			Firewall: fw.name,
		})
		if err != nil {
			log.Warn("failed to delete firewall rule", "name", fw.name, "error", err)
			return err
		}
		if err := op.Wait(ctx); err != nil {
			log.Warn("failed waiting for firewall rule deletion", "name", fw.name, "error", err)
			return err
		}
		log.Info("firewall rule deleted", "name", fw.name)
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
