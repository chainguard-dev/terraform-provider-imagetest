package emitter

import (
	"fmt"
	"io"
)

func New(w io.Writer, indent string) *Emitter {
	e := new(Emitter)
	e.w = w
	e.depth = 0
	e.indent = stob(indent)
	return e
}

type Emitter struct {
	w      io.Writer
	depth  int
	indent []byte
}

/*------------------------------------------------------------------------------
 * Indentation Handling
 *----------------------------------------------------------------------------*/

// Move the indent level in one (->).
func (self *Emitter) In() *Emitter {
	self.depth++
	return self
}

// Move the indent level out one (<-).
func (self *Emitter) Out() *Emitter {
	if self.depth <= 0 {
		return self
	}
	self.depth--
	return self
}

// Write the indent to the wrapped io.Writer.
func (self *Emitter) Indent() *Emitter {
	for range self.depth {
		self.w.Write(self.indent)
		// fmt.Fprint(self.w, self.indent)
	}
	return self
}

/*------------------------------------------------------------------------------
 * "Primitives"
 * ---------------
 * All other write functions use these under the hood.
 *----------------------------------------------------------------------------*/

// Write a string to the underlying io.Writer
func (self *Emitter) Write(s string) *Emitter {
	fmt.Fprint(self.w, s)
	return self
}

// Write a formatted string to the underlying io.Writer
func (self *Emitter) Writef(s string, vs ...any) *Emitter {
	fmt.Fprintf(self.w, s, vs...)
	return self
}

/*------------------------------------------------------------------------------
 * QoL Functions
 *----------------------------------------------------------------------------*/

// Write a string to the underlying io.Writer followed by a newline ('\n').
func (self *Emitter) Writeln(s string) *Emitter {
	self.Write(s)
	self.Write("\n")
	return self
}

// Write a formatted string to the underlying io.Writer followed by a newline
// ('\n').
func (self *Emitter) Writefln(s string, vs ...any) *Emitter {
	self.Writef(s, vs...)
	self.Write("\n")
	return self
}

// Write a newline to the underlying io.Writer.
func (self *Emitter) Newline() *Emitter {
	self.Write("\n")
	return self
}
