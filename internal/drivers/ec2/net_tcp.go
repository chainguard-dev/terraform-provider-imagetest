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
// If an error is returned by this function it will be 'context.Deadline'.
func waitTCP(ctx context.Context, host string, port uint16) error {
	log := clog.FromContext(ctx).With("host", host, "port", port)
	log.Debug("beginning wait for EC2 instance to become reachable via SSH")
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

func tcpPortOpen(ctx context.Context, target string) bool {
	log := clog.FromContext(ctx).With("target", target)
	log.Debug("checking target TCP port reachability")
	conn, err := dialer.DialContext(ctx, "tcp", target)
	if err != nil {
		log.Debug("target is not yet reachable", "error", err)
		return false
	}
	if err := conn.Close(); err != nil {
		log.Warn("encountered error closing TCP connection", "error", err)
	}
	log.Debug("target is now reachable")
	return true
}
