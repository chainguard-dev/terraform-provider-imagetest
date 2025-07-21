package mock

import (
	"bufio"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

// asyncRead continuously reads from the provided 'io.Reader', string-converting
// any read data and passing it through the returned channel.
func asyncRead(t *testing.T, r io.Reader) <-chan string {
	br := bufio.NewReader(r)
	ch := make(chan string, 64)
	go func(ch chan<- string) {
		defer close(ch)
		for {
			s, err := br.ReadString('\n')
			if err != nil {
				require.ErrorIs(t, err, io.EOF)
				return
			}
			ch <- string(s)
		}
	}(ch)
	return ch
}
