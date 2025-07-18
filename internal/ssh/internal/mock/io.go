package mock

import (
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

// asyncRead continuously reads from the provided 'io.Reader', string-converting
// any read data and passing it through the returned channel.
func asyncRead(t *testing.T, r io.Reader) <-chan string {
	ch := make(chan string, 64)
	go func(ch chan<- string) {
		buf := make([]byte, 1024)
		defer close(ch)
		for {
			n, err := r.Read(buf)
			if err != nil {
				require.ErrorIs(t, err, io.EOF)
				return
			}
			if n == 0 {
				continue
			}
			ch <- string(buf[:n])
		}
	}(ch)
	return ch
}
