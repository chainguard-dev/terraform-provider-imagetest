package ec2

import (
	"context"
	"net"
	"strconv"
	"time"

	"github.com/chainguard-dev/clog"
)

const portSSH = 22

// waitTCP waits for a TCP port to become reachable on provided target 'host'.
//
// The only error returned by this function is 'context.Deadline'.
func waitTCP(ctx context.Context, host string, port uint16) error {
	log := clog.FromContext(ctx).With("host", host, "port", port)
	target := net.JoinHostPort(host, strconv.Itoa(int(port)))
	for {
		select {
		case <-ctx.Done():
			log.Debug("hit deadline waiting for the EC2 instance to come up")
			return context.DeadlineExceeded
		case <-time.After(100 * time.Millisecond):
			log.Debug("checking TCP port reachability")
			// Check TCP port reachability
			if tcpPortOpen(ctx, target) {
				return nil
			}
		}
	}
}

var dialer = &net.Dialer{
	Timeout: 3 * time.Second,
}

// tcpPortOpen checks TCP reachability of the provided 'target', which must be a
// 'host:port' representation of a target we want to probe.
func tcpPortOpen(ctx context.Context, target string) bool {
	log := clog.FromContext(ctx).With("target", target)
	log.Debug("checking target TCP port reachability")
	conn, err := dialer.DialContext(ctx, "tcp", target)
	if err != nil {
		return false
	}
	if err := conn.Close(); err != nil {
		log.Warn("encountered error closing TCP connection", "error", err)
	}
	log.Debug("target is reachable")
	return true
}
