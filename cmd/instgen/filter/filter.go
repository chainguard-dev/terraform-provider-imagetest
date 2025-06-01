package filter

type Filter[T any] func(t T) bool

// And sends provided instance `T` to each `Filter[T]`, logically ANDing the
// results. That is, if ANY filters return false the result of `And` will be
// false.
//
// NOTE: If NO filters are provided the result will be true.
func And[T any](t T, filters ...Filter[T]) bool {
	if len(filters) == 0 {
		return true
	}

	for _, filter := range filters {
		if !filter(t) {
			return false
		}
	}

	return true
}

// Or sends provided instance `T` to each `Filter[T]`, logically ORing the
// results. That is, the first filter which returns true triggers an early
// true return.
//
// NOTE: If NO filters are provided the result will be true.
func Or[T any](t T, filters ...Filter[T]) bool {
	if len(filters) == 0 {
		return true
	}

	for _, filter := range filters {
		if filter(t) {
			return true
		}
	}

	return false
}
