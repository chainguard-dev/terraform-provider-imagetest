package ssh

import (
	"bytes"
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateKeyPair(t *testing.T) {
	// Generate a keypair, sign a message and verify its digest to confirm the
	// keypair is valid
	t.Run("validate-matching-keys", func(t *testing.T) {
		pair, err := NewED25519KeyPair()
		require.NoError(t, err)
		const msg = "Hello, worldd"
		sig, err := pair.Private.Sign([]byte(msg))
		require.NoError(t, err)
		require.True(t, len(sig) > 0)
		assert.True(t, pair.Public.Verify([]byte(msg), sig))
	})
	t.Run("validate-public-key-marshal", func(t *testing.T) {
		pair, err := NewED25519KeyPair()
		require.NoError(t, err)
		// Marshal the public key to the 'openssh' authorized_keys file format
		pub, err := pair.Public.MarshalOpenSSH()
		require.NoError(t, err)
		// Verify the marshaled format of the public key
		//
		// Expect: Standard 'ssh-ed25519' key prefix
		pub = expectPrefix(t, pub, []byte("ssh-ed25519 ")...)
		// Expect: trailing newline
		pub = expectSuffix(t, pub, '\n')
		// Base64-decode the remainder
		dec := make([]byte, len(pub)*2)
		n, err := base64.StdEncoding.Decode(dec, pub)
		require.NoError(t, err)
		require.True(t, n > 0)
		dec = dec[:n]
		// Expect: 3 NUL bytes, vertical tab
		dec = expectPrefix(t, dec, 0, 0, 0, '\x0b')
		// Expect 'ssh-ed25519'
		dec = expectPrefix(t, dec, []byte("ssh-ed25519")...)
		// Expect: 3 NUL bytes, space
		dec = expectPrefix(t, dec, 0, 0, 0, ' ')
		// Expect: 32-bytes of remaining data
		assert.True(t, len(dec) == 32)
	})
	t.Run("validate-private-key-marshal", func(t *testing.T) {
		pair, err := NewED25519KeyPair()
		require.NoError(t, err)
		// Marshal the private key to the
		priv, err := pair.Private.MarshalOpenSSH("test")
		require.NoError(t, err)
		// Expect: '-----BEGIN OPENSSH PRIVATE KEY-----' header
		priv = expectPrefix(t, priv, []byte("-----BEGIN OPENSSH PRIVATE KEY-----")...)
		// Expect: '-----END OPENSSH PRIVATE KEY-----' trailer with newline
		priv = expectSuffix(t, priv, []byte("-----END OPENSSH PRIVATE KEY-----\n")...)
		// Base64-decode the remainder, make sure there's no error and non-empty data
		dec, err := base64.StdEncoding.DecodeString(string(priv))
		require.NoError(t, err)
		require.True(t, len(dec) > 0)
		parts := bytes.Split(dec, []byte("ssh-ed25519"))
		// We expect 3 parts:
		// 1. Standard 'openssh-key-v1' header
		// 2. Public key (with padding)
		// 3. Private key (with padding)
		require.True(t, len(parts) == 3)
		expectPrefix(t, parts[0], []byte("openssh-key-v1\x00\x00\x00\x00")...)
		assert.True(t, bytes.Contains(parts[1], pair.Public.key))
		assert.True(t, bytes.Contains(parts[2], pair.Private.key))
	})
}

// expectPrefix looks for a sequence of bytes 'expects' to occur from the start
// of the provided byte slice 'input'.
//
// If all bytes are present, they are sliced off the front of the slice and
// returned
func expectPrefix(t *testing.T, input []byte, expects ...byte) []byte {
	t.Helper()
	require.True(t, len(input) >= len(expects))
	for i, expect := range expects {
		got := input[i]
		assert.Equal(t, expect, got, "expected [%c], got [%c]", expect, got)
	}
	return input[len(expects):]
}

// expectPrefix looks for a sequence of bytes 'expects' to occur from the end
// of the provided byte slice 'input'.
//
// If all bytes are present, they are sliced off the back of the slice and
// returned
func expectSuffix(t *testing.T, input []byte, expects ...byte) []byte {
	t.Helper()
	require.True(t, len(input) >= len(expects))
	for i, expect := range expects {
		got := input[len(input)-len(expects)+i]
		assert.Equal(t, expect, got, "expected [%c], got [%c]", expect, got)
	}
	return input[:len(input)-len(expects)]
}
