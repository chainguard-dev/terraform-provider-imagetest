package filter

// Post filters are handled in this codebase following a call to
// 'DescribeInstanceTypes'.
var Post struct {
	Storage filtersStoragePost
	GPU     filtersGPUPost
}
