package mock

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

type (
	// server represents an SSH server.
	//
	// server is constructed by 'NewServer', can be started (begin listening and
	// serving connections) by calling its 'ListenAndServe' method. When finished,
	// a call to 'Shutdown' will gracefully shutdown the TCP listener.
	server struct {
		// The SSH server configuration.
		//
		// These options may be modified _prior_ to calling 'ListenAndServe',
		// modifying after will have no effect.
		Config *ssh.ServerConfig

		// Holds the closure we'll use to shut down the Server.
		cancel context.CancelFunc

		// The TCP port to listen on.
		port uint16

		// 'Waiter' is a 'sync.WaitGroup'-like construct, save that it accepts a
		// 'context.Context' on its 'Done' method, supporting deadlines.
		wait Waiter
	}
	// PubKeyCallback is the function called when the server receives an
	// authentication attempt via public key. Any non-nil error returned will
	// immediately abort the connection.
	PubKeyCallback func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error)

	// ReqChannel produces all *ssh.Requests, which are out-of-band well-known
	// marshaled data structures which arrive from either a specific channel or
	// the ssh.SSHConn.
	ReqChannel <-chan *ssh.Request

	// MsgChannel produces all messages which arrive directly over the ssh
	// connection (think simple writes to stdin on the client's side).
	MsgChannel <-chan string
)

func NewServer(t *testing.T, port uint16, signer ssh.Signer, fn PubKeyCallback) (*server, error) {
	if t == nil {
		return nil, fmt.Errorf("no *testing.T provided in call to NewServer")
	}
	require.NotNil(t, fn != nil, "a non-nil public key callback is required")
	require.NotNil(t, signer, "a non-nil ssh.Signer is required")
	// Init the SSH server config, add the host key
	config := &ssh.ServerConfig{
		PublicKeyCallback: fn,
	}
	config.AddHostKey(signer)
	return &server{
		Config: config,

		wait: NewWaiter(),
		port: port,
	}, nil
}

func (self *server) ListenAndServe(t *testing.T, ctx context.Context) (ReqChannel, MsgChannel, error) {
	// Wrap the context with a cancellation.
	//
	// We'll use this 'context.CancelFunc' to shutdown the server in the
	// 'Shutdown' method.
	ctx, self.cancel = context.WithCancel(ctx)
	// Init the TCP listener
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{
		Port: int(self.port),
	})
	require.NoError(t, err, "failed to listen on TCP/%d: %s", self.port, err)
	// Init the channel we'll send a copy of all SSH requests into
	outReqChan := make(chan *ssh.Request, 64)
	outMsgChan := make(chan string, 64)
	// Begin serving SSH requests
	self.wait.Add()
	go self.serve(t, ctx, listener, outReqChan, outMsgChan)
	return outReqChan, outMsgChan, nil
}

func (self *server) serve(
	t *testing.T,
	ctx context.Context,
	listener *net.TCPListener,
	outReqChan chan<- *ssh.Request,
	outMsgChan chan<- string,
) {
	defer self.wait.Done()
	for {
		select {
		case <-ctx.Done():
			// Close all channels.
			close(outReqChan)
			close(outMsgChan)
			// Close TCP listener.
			require.NoError(t, listener.Close())
			return
		default:
			// Don't block forever.
			listener.SetDeadline(time.Now().Add(100 * time.Millisecond))
			conn, err := listener.AcceptTCP()
			if err != nil {
				var operr *net.OpError
				if errors.As(err, &operr) && operr.Timeout() {
					continue
				}
			}
			require.NoError(t, err)
			self.wait.Add()
			go self.handleTCPConn(t, ctx, conn, outReqChan, outMsgChan)
		}
	}
}

// handleTCPConn attempts an SSH handshake over the provided '*net.TCPConn'.
//
// If successful it will continuously drain the inbound channel requests
// channel, accepting 'session' channel requests and spawning a channel handler
// in a separate Goroutine.
//
// See 'handleChannel' for more details.
func (self *server) handleTCPConn(
	t *testing.T,
	ctx context.Context,
	conn *net.TCPConn,
	outReqChan chan<- *ssh.Request,
	outMsgChan chan<- string,
) {
	defer self.wait.Done()
	// Perform the SSH handshake.
	sshConn, inChanReqChan, inReqChan, err := ssh.NewServerConn(
		conn,
		self.Config,
	)
	require.NoError(t, err)
	defer sshConn.Close()
	// Discard everything from the request chan (we don't care about anything
	// in here).
	go func() {
		// This just ACKs all requests, if one was received and requested a reply.
		ssh.DiscardRequests(inReqChan)
	}()
	// Field all new channel requests.
	for {
		select {
		case <-ctx.Done():
			return
		case newChannelRequest := <-inChanReqChan:
			// Reject non-session channels
			if newChannelRequest.ChannelType() != "session" {
				newChannelRequest.Reject(ssh.UnknownChannelType, "unknown channel type")
				continue
			}
			// Accept 'session' channels.
			channel, inReqChan, err := newChannelRequest.Accept()
			require.NoError(t, err)
			// Continuously read from 'channel', relaying messages read back over
			// a channel.
			inMsgChan := asyncRead(t, channel)
			// Handle the channel in a separate Goroutine.
			go self.handleChannel(t, ctx, channel, inMsgChan, inReqChan, outReqChan, outMsgChan)
		}
	}
}

// handleChannel processes all in-band and out-of-band messages delivered over
// its 'ssh.Channel'.
//
// INBOUND REQUESTS from 'inReqChan' of type 'exec' are ACKed, lightly processed
// and delivered back over 'outReqChan' for the caller of this package to
// inspect. All other message types will panic (none of these are required
// today).
//
// INBOUND MESSAGES from 'inMsgChan' are lightly processed and delivered back
// over 'outMsgChan' for the caller of this package to inspect.
//
// This function exits when either the 'context.Context' is marked done, or
// the 'inMsgChan' is closed, whichever comes first. On exit, the SSH channel
// is closed. See 'asyncRead' for more details on 'inMsgChan' closure.
func (self *server) handleChannel(
	t *testing.T,
	ctx context.Context,
	channel ssh.Channel,
	inMsgChan <-chan string,
	inReqChan <-chan *ssh.Request,
	outReqChan chan<- *ssh.Request,
	outMsgChan chan<- string,
) {
	defer func() {
		self.wait.Done()
		require.NoError(t, channel.Close())
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case channelRequest := <-inReqChan:
			// An '*ssh.Request' is a request from the client being sent _in_ the
			// established channel. 'Request' in this context is essentially a message
			// of a well-known structure. For example, 'exec' is the request message
			// type which executes a program, 'shell' just means start this user's
			// default shell.
			switch channelRequest.Type {
			case "exec":
				log.Debug("received an 'exec' channel request")
				// If the request wants a reply, it _must_ be replied to.
				if channelRequest.WantReply {
					log.Debug("ACKing channel request")
					// Since these are "Channel-specific" 'ssh.Request's, the returned
					// payload is ignored.
					err := channelRequest.Reply(true, nil)
					require.NoError(t, err)
				}
				// All SSH 'exec' commands expect a response payload in uint32 byte
				// order where the last 8-bits indicates the command's exit code.
				//
				// NOTE: This could be extended to allow for deeper testing with mock
				// non-zero exit codes.
				_, err := channel.SendRequest("exit-status", false, marshalExitStatus(0))
				require.NoError(t, err)
				// For convenience sake, remove all leading control characters in the
				// payload.
				channelRequest.Payload = bytes.TrimLeftFunc(channelRequest.Payload, func(r rune) bool {
					return r < 0x20
				})
				outReqChan <- channelRequest
			case "shell":
				log.Error("received a 'shell' channel request, but this request type is not implemented")
				continue
			case "env":
				log.Error("received an 'env' channel request, but this request type is not implemented")
				continue
			}
		case channelMessage, more := <-inMsgChan:
			// channelMessage means raw data sent over the wire from our client
			// that isn't a well-known 'Request'. An example would be stdin to send
			// to a running process.
			//
			// For convenience sake on the receiver's side, we send each line
			// individually.
			//
			// Every message will have a trailing newline, trim that off first.
			channelMessage = strings.TrimSpace(channelMessage)
			for line := range strings.SplitSeq(channelMessage, "\n") {
				// Each line will be prefixed with some control codes, trim those.
				line = strings.TrimFunc(line, func(r rune) bool {
					return r < 0x20
				})
				// Ignore blank lines.
				if line == "" {
					continue
				}
				log.Debug("sending channel message", "message", line)
				outMsgChan <- line
			}
			// When the 'asyncRead' Goroutine closes this channel ('io.EOF' received),
			// that means the client has signalled a disconnect and we're all done.
			if !more {
				return
			}
		}
	}
}

var ErrServerNotStarted = fmt.Errorf(
	"shutdown called without a call to 'ListenAndServe' first",
)

// Shutdown calls the 'context.CancelFunc' and waits for all Goroutines to exit.
func (self *server) Shutdown(ctx context.Context) error {
	if self.cancel == nil {
		return ErrServerNotStarted
	}
	self.cancel()
	return self.wait.WaitContext(ctx)
}
