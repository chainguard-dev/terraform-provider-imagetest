package emitter

import (
	"bytes"
	"fmt"
	"testing"

	"gotest.tools/v3/assert"
)

func TestEmitter(t *testing.T) {
	const initsz = 256
	buf := bytes.NewBuffer(make([]byte, 0, initsz))
	e := New(buf, "\t")

	expect := func(v string, newline bool) {
		t.Helper()
		if newline {
			assert.Equal(t, buf.String(), v+"\n")
		} else {
			assert.Equal(t, buf.String(), v)
		}
		buf.Reset()
	}

	// Write(string)
	input := "Hello, %s!"
	e.Write(input)
	expect(input, false)

	// Writeln(string)
	e.Writeln(input)
	expect(input, true)

	// Writef(string, string)
	vs := []any{"World"}
	e.Writef(input, vs...)
	expect(fmt.Sprintf(input, vs...), false)

	// Writefln(string, string)
	e.Writefln(input, vs...)
	expect(fmt.Sprintf(input, vs...), true)

	// Newline()
	e.Newline()
	expect("", true)
}
