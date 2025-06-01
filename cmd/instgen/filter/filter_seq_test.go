package filter

import (
	"iter"
	"testing"

	"gotest.tools/v3/assert"
)

func TestAndSeq(t *testing.T) {
	for b := range AndSeq(seqAlpha(), lowercase) {
		assert.Check(t, b >= 'a' && b <= 'z')
	}
	// Break after first result
	//
	// Stat padding for coverage [:<
	for b := range AndSeq(seqAlpha(), lowercase) {
		assert.Check(t, b >= 'a' && b <= 'z')
		break
	}
}

func TestOrSeq(t *testing.T) {
	// Filter out all uppercase bytes
	for b := range OrSeq(seqAlpha(), lowercase) {
		assert.Check(t, b >= 'a' && b <= 'z')
	}

	// Break after first result
	//
	// Stat padding for coverage [:<
	for b := range OrSeq(seqAlpha(), lowercase) {
		assert.Check(t, b >= 'a' && b <= 'z')
	}

	// Filter non-uppercase/lowercase bytes (should give us back the whole
	// alphabet in upper and lowercase)
	for b := range OrSeq(seqAlpha(), lowercase, uppercase) {
		assert.Check(t, b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z')
	}
}

func seqAlpha() iter.Seq[byte] {
	return func(yield func(byte) bool) {
		for i := 'a'; i < 'z'; i++ {
			if !yield(byte(i)) {
				return
			}
		}
		for i := 'A'; i < 'Z'; i++ {
			if !yield(byte(i)) {
				return
			}
		}
	}
}
