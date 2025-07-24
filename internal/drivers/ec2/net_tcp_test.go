package ec2

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWaitTCP(t *testing.T) {
	const host = "127.0.0.1"
	const port = 2222
	// Standard case (port open)
	ctx, cancel := context.WithCancel(t.Context())
	mockServer(t, ctx, port, 0)
	require.NoError(t, waitTCP(ctx, host, port))
	cancel()
	// Expect deadline
	ctx, cancel = context.WithTimeout(t.Context(), 500*time.Millisecond)
	require.ErrorIs(t, context.DeadlineExceeded, waitTCP(ctx, host, port))
	cancel()
}

func mockServer(t *testing.T, ctx context.Context, port uint16, startDelay time.Duration) {
	go func() {
		<-time.After(startDelay)
		listener, err := net.ListenTCP("tcp", &net.TCPAddr{
			IP:   net.IPv4(127, 0, 0, 1),
			Port: int(port),
		})
		require.NoError(t, err)
		defer func() { require.NoError(t, listener.Close()) }()
		for {
			select {
			case <-ctx.Done():
				return
			default:
				// 'listener.AcceptTCP()' will block forever, but we want to catch the
				// context being marked done as soon as possible, so we set a listener
				// deadline for now+100ms.
				require.NoError(t, listener.SetDeadline(time.Now().Add(100*time.Millisecond)))
				// Accept the next TCP connection.
				conn, err := listener.AcceptTCP()
				if err != nil {
					// If we hit the deadline, we'll get back a '*net.OpError'.
					if err, ok := err.(*net.OpError); ok && err.Timeout() {
						continue
					}
				}
				// On a clean connection, we just really need the ACK so we can close
				// it immediately.
				require.NoError(t, err)
				require.NoError(t, conn.Close())
			}
		}
	}()
}
