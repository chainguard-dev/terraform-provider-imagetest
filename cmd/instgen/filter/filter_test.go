package filter

import (
	"bytes"
	"testing"

	"gotest.tools/v3/assert"
)

func TestAnd(t *testing.T) {
	var b byte = 'A'

	// No filters (positive result)
	assert.Check(t, And(b))

	// Single filter, non-match
	assert.Check(t, !And(b, lowercase))

	// Single filter, match
	b = 'a'
	assert.Check(t, And(b, lowercase))

	// ANDed filters, match
	assert.Check(t, And(b, lowercase, isa))

	// ANDed filters, non-match
	b = 'b'
	assert.Check(t, !And(b, lowercase, isa))
}

func TestAnds(t *testing.T) {
	slice := []byte{'a', 'A', 'b', 'B', 'c', 'C', 'd', 'D'}

	// Nil/zero-length slice (returns input)
	assert.Check(t, len(Ands([]byte{})) == 0)
	assert.Check(t, len(Ands[[]byte](nil)) == 0)

	// No filters (returns input)
	assert.Check(t, bytes.Equal(slice, Ands(slice)))

	// Single filter, with matches
	result := Ands(slice, lowercase)
	assert.Check(t, bytes.Equal([]byte{'a', 'b', 'c', 'd'}, result))

	// ANDed filters, with match
	result = Ands(slice, lowercase, isa)
	assert.Check(t, bytes.Equal([]byte{'a'}, result))

	// ANDed filters, no match
	result = Ands(slice[1:], lowercase, isa)
	assert.Check(t, len(result) == 0)
}

func TestOr(t *testing.T) {
	var b byte = 'A'

	// No filters (positive result)
	assert.Check(t, Or(b))

	// Single filter, non-match
	assert.Check(t, !Or(b, lowercase))

	// Single filter, match
	b = 'a'
	assert.Check(t, Or(b, lowercase))

	// ORed filters, match
	assert.Check(t, Or(b, lowercase, isa))

	// ORed filters, non-match
	b = 'A'
	assert.Check(t, !Or(b, lowercase, isa))
}

func TestOrs(t *testing.T) {
	slice := []byte{'a', 'A', 'b', 'B', 'c', 'C', 'd', 'D'}

	// Single filter, with matches
	result := Ors(slice, lowercase)
	assert.Check(t, bytes.Equal([]byte{'a', 'b', 'c', 'd'}, result))

	// Nil/zero-length slice (returns input)
	assert.Check(t, len(Ors([]byte{})) == 0)
	assert.Check(t, len(Ors[[]byte](nil)) == 0)

	// No filters (returns input)
	assert.Check(t, bytes.Equal(slice, Ors(slice)))

	// ORed filters, with matches
	result = Ors(slice, lowercase, isa)
	assert.Check(t, bytes.Equal([]byte{'a', 'b', 'c', 'd'}, result))

	// ORed filters, no match
	result = Ors(slice[1:], uppercase)
	assert.Check(t, len(result) == 0)
}

// Byte is uppercase
func uppercase(b byte) bool {
	return b >= 'A' && b <= 'Z'
}

// Byte is lowercase
func lowercase(b byte) bool {
	return b >= 'a' && b <= 'z'
}

// Byte is 'a'
func isa(b byte) bool {
	return b == 'a'
}
