package mock

import (
	"bytes"
	"fmt"
	"testing"

	"golang.org/x/crypto/ssh"
)

var ErrUnauthorized = fmt.Errorf("public key is not authorized")

func PublicKeyCallback(t *testing.T, allowedPubKeys ...ssh.PublicKey) PubKeyCallback {
	marshaledPubKeys := make([][]byte, len(allowedPubKeys))
	for i := range len(marshaledPubKeys) {
		marshaledPubKeys[i] = allowedPubKeys[i].Marshal()
	}
	return func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		// Expect the "user" public key defined above on SSH connections
		keyMarshaled := key.Marshal()
		for _, marshaledPubKey := range marshaledPubKeys {
			if bytes.Equal(marshaledPubKey, keyMarshaled) {
				return nil, nil
			}
		}
		// require.Contains(t, marshaledPubKeys, key.Marshal(), "public key is not authorized", string(key.Marshal()))
		// require.True(t, bytes.Equal(userPubKey.Marshal(), key.Marshal()))
		return nil, ErrUnauthorized
	}
}
