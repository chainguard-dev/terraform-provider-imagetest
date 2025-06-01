package filter

import "slices"

// Ands applies each `Filter[T]` to each provided instance `T`, returning the
// filtered results.
//
// Under the hood `And` is called for each `T`. See `And` for more details.
//
// In the event of no matches, a zero-length slice is returned.
func Ands[T any](ts []T, filters ...Filter[T]) []T {
	if len(ts) == 0 {
		return ts
	}

	if len(filters) == 0 {
		return ts
	}

	return slices.DeleteFunc(ts, func(t T) bool {
		return !And(t, filters...)
	})
}

// Ors applies each `Filter[T]` to each provided instance `T`, returning the
// filtered results.
//
// Under the hood `Or` is called for each `T`. See `Or` for more details.
//
// In the event of no matches, a zero-length slice is returned.
func Ors[T ~[]E, E any](ts T, filters ...Filter[E]) T {
	if len(ts) == 0 {
		return ts
	}

	if len(filters) == 0 {
		return ts
	}

	return slices.DeleteFunc(ts, func(t E) bool {
		return !Or(t, filters...)
	})
}
