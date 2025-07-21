// ssh implements a facade over the 'x/crypto/ssh' package, simplifying the
// following workflows:
//   - ED25519 key generation, conversion and marshaling
//   - SSH client construction
//   - SSH client command execution and sequencing
//
// NOTE: ALL errors returned by this package will be wrapped with well-known (
// 'errors.Is(...') errors.
package ssh
