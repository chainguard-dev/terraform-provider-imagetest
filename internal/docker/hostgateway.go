package docker

import (
	"context"
	"fmt"
	"strings"

	"github.com/moby/moby/client"
)

// HostListenIP returns the host-side IP a process should bind to so that
// containers can reach it via `host.docker.internal:host-gateway`.
//
// Desktop style runtimes (Docker Desktop, OrbStack) forward that name to the
// host's loopback interface. A native Linux daemon resolves it to the default
// bridge gateway, so the process has to listen on that address instead.
func (d *Client) HostListenIP(ctx context.Context) (string, error) {
	info, err := d.inner.Info(ctx, client.InfoOptions{})
	if err != nil {
		return "", fmt.Errorf("inspecting docker daemon: %w", err)
	}

	osName := strings.ToLower(info.Info.OperatingSystem)
	if strings.Contains(osName, "docker desktop") || strings.Contains(osName, "orbstack") {
		return "127.0.0.1", nil
	}

	nw, err := d.inner.NetworkInspect(ctx, "bridge", client.NetworkInspectOptions{})
	if err != nil {
		return "", fmt.Errorf("inspecting default bridge network: %w", err)
	}
	for _, cfg := range nw.Network.IPAM.Config {
		if gw := cfg.Gateway; gw.IsValid() && gw.Is4() {
			return gw.String(), nil
		}
	}
	return "", fmt.Errorf("default bridge network has no IPv4 gateway")
}
