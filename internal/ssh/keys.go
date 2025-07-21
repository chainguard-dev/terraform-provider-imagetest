package ssh

// keys.go implements a facade over standard library package 'crypto/ed25519'
// for more ergonomic interactions with ED25519 public and private keys in the
// context of SSH connections.
//
// Working with SSH clients and servers, there are many formats and key
// representations that are commonly needed and achieving these formats requires
// calls to four standard library packages ('crypto', 'crypto/ed25519',
// 'x/crypto/ssh', 'encoding/pem') and entirely too much knowledge of SSH as a
// protocol.
//
// All keys begin life as a 'crypto/*' (in this package's case 'crypto/ed25519')
// then...
//
// FOR CLIENTS:
// - For outbound connection authorization you'll need an 'ssh.PublicKey'.
// - For outbound connection message signing you'll need an 'ssh.Signer'.
// - For OpenSSH representations of your public key, you'll need to marshal it
//   to the OpenSSH-specific ('authorized_keys') format.
//
// FOR SERVERS:
// - For inbound connections you'll need your public key as an 'ssh.PublicKey' (
//   host key).
// - For connection message signing, you'll need an 'ssh.Signer'.
// - For OpenSSH representations of your private key you'll need to marshal it
//   to the OpenSSH-specific format (PEM with an 'OPENSSH' block header).
//
// For convenience, the standard 'Sign' and 'Verify' methods of the
// 'crypto/ed25519' keys are also wrapped.
//
// NOTE: It may be confusing but 'x/crypto/ssh' doesn't have an implementation
// of a 'PrivateKey' (though it does have a 'PublicKey'). The 'Signer' interface
// fulfills all the roles of a private key within the 'x/crypto/ssh' package.

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"

	"golang.org/x/crypto/ssh"
)

var (
	ErrKeyGen         = fmt.Errorf("failed to generate a 'crypto/ed25519' keypair")
	ErrPubKeyConv     = fmt.Errorf("failed to convert the 'ed25519.PublicKey' to 'ssh.PublicKey'")
	ErrPrivKeyConv    = fmt.Errorf("failed to convert the 'ed25519.PrivateKey' to an 'ssh.Signer'")
	ErrPubKeyMarshal  = fmt.Errorf("failed to marshal the 'ssh.PublicKey' to OpenSSH format")
	ErrPrivKeyMarshal = fmt.Errorf("failed to marshal the 'ssh.PrivateKey' to OpenSSH format")
	ErrPEMEncode      = fmt.Errorf("failed to PEM-encode the ssh.PrivateKey")
)

// Generates a 'crypto/ed25519' public+private key pair, as an 'ED25519KeyPair'.
func NewED25519KeyPair() (ED25519KeyPair, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return ED25519KeyPair{}, fmt.Errorf("%w: %w", ErrKeyGen, err)
	}
	return ED25519KeyPair{
		Public: ED25519PublicKey{
			key: pub,
		},
		Private: ED25519PrivateKey{
			key: priv,
		},
	}, nil
}

type ED25519KeyPair struct {
	Public  ED25519PublicKey
	Private ED25519PrivateKey
}

type ED25519PublicKey struct {
	key ed25519.PublicKey
}

// Verifies signature hash 'sig' against signed message 'msg' using the ed25519
// public key.
func (pubKey ED25519PublicKey) Verify(msg, sig []byte) bool {
	return ed25519.Verify(pubKey.key, msg, sig)
}

// Converts the 'ed25519.PublicKey' to an 'ssh.PublicKey'.
func (pubKey ED25519PublicKey) ToSSH() (ssh.PublicKey, error) {
	pub, err := ssh.NewPublicKey(pubKey.key)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrPubKeyConv, err)
	}
	return pub, nil
}

// Marshals the 'ed25519.PublicKey' to the OpenSSH ('authorized_keys') format.
func (pubKey ED25519PublicKey) MarshalOpenSSH() ([]byte, error) {
	// Convert the 'ed25519.PublicKey' to an 'ssh.PublicKey'.
	publicKey, err := pubKey.ToSSH()
	if err != nil {
		return nil, err
	}
	// Marshal the public key to the OpenSSH format.
	marshaled := ssh.MarshalAuthorizedKey(publicKey)
	if marshaled == nil {
		return nil, ErrPubKeyMarshal
	}
	return marshaled, nil
}

type ED25519PrivateKey struct {
	key ed25519.PrivateKey
}

// Signs a message with plain* ED25519 using the 'ed25519.PrivateKey'.
//
// * Plain means the message is not SHA-512 pre-hashed ('ed25519ph').
func (privKey ED25519PrivateKey) Sign(msg []byte) ([]byte, error) {
	return privKey.key.Sign(rand.Reader, msg, crypto.Hash(0))
}

// Marshals the 'ed25519.PrivateKey' to the OpenSSH format.
func (privKey ED25519PrivateKey) MarshalOpenSSH(comment string) ([]byte, error) {
	// Marshal the 'ed25519.PrivateKey' to the standard OpenSSH format.
	priv, err := ssh.MarshalPrivateKey(privKey.key, comment)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrPrivKeyMarshal, err)
	}
	// Encode the 'pem.Block'.
	encoded := pem.EncodeToMemory(priv)
	if encoded == nil {
		return nil, fmt.Errorf("%w: %w", ErrPEMEncode, err)
	}
	return encoded, nil
}

// Converts the 'ed25519.PrivateKey' to an 'ssh.Signer'.
func (privKey ED25519PrivateKey) ToSSH() (ssh.Signer, error) {
	return ssh.NewSignerFromKey(privKey.key)
}

var ErrSSHFailedKeyParse = fmt.Errorf("failed to parse SSH private key")

// ParseKey attempts to parse the provided 'key' value as a PEM-encoded OpenSSH
// format private key.
//
// If 'phrase' is nil or an empty slice, the key parse will be attempted
// assuming no encryption.
// If 'phrase' is provided, the key will be parsed assuming encryption. If the
// parse fails with the key it will be reattempted assuming no encryption.
func ParseKey(key, phrase []byte) (ssh.Signer, error) {
	if len(key) == 0 {
		return nil, nil
	}
	// If we received a passphrase, attempt parsing the encrypted key first
	if len(phrase) > 0 {
		// This looks a little funky because we _only_ want to return here if the
		// error is nil
		signer, err := ssh.ParsePrivateKeyWithPassphrase(key, phrase)
		if err == nil {
			return signer, nil
		}
		// If we received an x509.IncorrectPasswordError, reattempt parsing
		// without the passphrase (key might not be encrypted), otherwise return
		// all other errors
		if !errors.Is(err, x509.IncorrectPasswordError) {
			return nil, fmt.Errorf("%w: %w", ErrSSHFailedKeyParse, err)
		}
	}
	// Attempt parsing a plaintext key
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSSHFailedKeyParse, err)
	}
	return signer, nil
}
