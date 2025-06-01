package filter

import (
	"iter"
)

// AndSeq accepts an iter.Seq[T] returning an iter.Seq[T] which produces only
// results which logical-AND-satisfy all provided filters.
func AndSeq[T any](it iter.Seq[T], filters ...Filter[T]) iter.Seq[T] {
	return func(yield func(T) bool) {
		for t := range it {
			if filters == nil || len(filters) == 0 || And(t, filters...) {
				if !yield(t) {
					return
				}
			}
		}
	}
}

// AndSeqs accepts an iter.Seq[T], consuming all results and applying filters
// at each call of the iterator, returning the positively matched results
// as a slice.
func AndSeqs[T any](it iter.Seq[T], filters ...Filter[T]) []T {
	if it == nil {
		return nil
	}

	var ts []T
	for t := range it {
		if len(filters) == 0 || And(t, filters...) {
			ts = append(ts, t)
		}
	}

	return ts
}

// AndSeq accepts an iter.Seq[T] returning an iter.Seq[T] which produces only
// results which logical-AND-satisfy all provided filters.
func OrSeq[T any](it iter.Seq[T], filters ...Filter[T]) iter.Seq[T] {
	return func(yield func(T) bool) {
		for t := range it {
			if len(filters) == 0 || Or(t, filters...) {
				if !yield(t) {
					return
				}
			}
		}
	}
}

// OrSeqs accepts an iter.Seq[T], consuming all results and applying filters
// at each call of the iterator, returning the positively matched results
// as a slice.
func OrSeqs[T any](it iter.Seq[T], filters ...Filter[T]) []T {
	if it == nil {
		return nil
	}

	var ts []T
	for t := range it {
		if len(filters) == 0 || Or(t, filters...) {
			ts = append(ts, t)
		}
	}

	return Ors(ts)
}
