// Package harness provides various harnesses that features can use to test
// against. The harnesses act on the StepFn of the test to both build the
// testing environment (such as kubernetes clusters), and to execute the test
// itself. For example, the kubernetes harness will use the StepFn to create a
// kubernetes cluster, and then execute the test against that cluster.
//
// TODO: This package is a mess right now with all sorts of os/exec nastiness.
// Factor this out into proper use of the docker sdk when the api stabilizes.
package harnesses
