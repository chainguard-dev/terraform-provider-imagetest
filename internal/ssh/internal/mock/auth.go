package mock

import (
	"bytes"
	"fmt"
	"testing"

	"golang.org/x/crypto/ssh"
)

var ErrUnauthorized = fmt.Errorf("public key is not authorized")

// PublicKeyCallback returns a closure for use with an 'ssh.ServerConfig' to
// perform validation of offered public keys from inbound SSH connections
// against the public keys provided in 'allowedPubKeys'.
func PublicKeyCallback(t *testing.T, allowedPubKeys ...ssh.PublicKey) PubKeyCallback {
	marshaledPubKeys := make([][]byte, len(allowedPubKeys))
	for i := range len(marshaledPubKeys) {
		marshaledPubKeys[i] = allowedPubKeys[i].Marshal()
	}
	return func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		// Expect the "user" public key to exist in 'allowedPubKeys'.
		keyMarshaled := key.Marshal()
		for _, marshaledPubKey := range marshaledPubKeys {
			if bytes.Equal(marshaledPubKey, keyMarshaled) {
				return nil, nil
			}
		}
		return nil, ErrUnauthorized
	}
}
