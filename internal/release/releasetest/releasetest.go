// Package releasetest makes a studio for tests: a root key, a release key the root vouches for,
// and signatures by it, in the form the real ones have. The packages that fetch and install
// releases test against it, so that what they are held to is the real check and not a stand-in
// for it. It is no part of tty1: only tests import it.
package releasetest

import (
	"crypto/ed25519"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"testing"
	"time"

	"usr.games/tty1/internal/release"
)

// who is the identity and the purpose of a release's signature.
const who = "release@usr.games"

// Studio is a root, and a release key with the root's certificate.
type Studio struct {
	Signers release.Signers // the list that trusts this studio's root

	key  ed25519.PrivateKey
	cert []byte
}

// field writes one text or blob as SSH does: four bytes of length, then the bytes.
func field(b, s []byte) []byte {
	return append(binary.BigEndian.AppendUint32(b, uint32(len(s))), s...)
}

// wire returns a public key in the form SSH writes it.
func wire(pub ed25519.PublicKey) []byte { return field(field(nil, []byte("ssh-ed25519")), pub) }

// New makes a studio whose certificate is good from yesterday until tomorrow.
func New(t testing.TB) *Studio {
	t.Helper()
	rootPub, root, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	pub, key, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	list := who + ` cert-authority,namespaces="` + who + `" ssh-ed25519 ` + base64.StdEncoding.EncodeToString(wire(rootPub)) + "\n"
	signers, err := release.ParseSigners([]byte(list))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	c := field(nil, []byte("ssh-ed25519-cert-v01@openssh.com"))
	c = field(c, []byte("a nonce"))
	c = field(c, pub)
	c = binary.BigEndian.AppendUint64(c, 1)                                     // the serial number
	c = binary.BigEndian.AppendUint32(c, 1)                                     // a user's certificate
	c = field(c, []byte("made for a test"))                                     // its own name
	c = field(c, field(nil, []byte(who)))                                       // good for one identity
	c = binary.BigEndian.AppendUint64(c, uint64(now.Add(-24*time.Hour).Unix())) // from
	c = binary.BigEndian.AppendUint64(c, uint64(now.Add(24*time.Hour).Unix()))  // until
	c = field(c, nil)                                                           // no conditions
	c = field(c, nil)                                                           // no extensions
	c = field(c, nil)                                                           // reserved
	c = field(c, wire(rootPub))
	c = field(c, field(field(nil, []byte("ssh-ed25519")), ed25519.Sign(root, c)))
	return &Studio{Signers: signers, key: key, cert: c}
}

// Sign returns the studio's signature of message, as `ssh-keygen -Y sign` writes one.
func (s *Studio) Sign(message []byte) []byte {
	digest := sha512.Sum512(message)
	over := field([]byte("SSHSIG"), []byte(who))
	over = field(over, nil) // reserved
	over = field(over, []byte("sha512"))
	over = field(over, digest[:])

	b := binary.BigEndian.AppendUint32([]byte("SSHSIG"), 1)
	b = field(b, s.cert)
	b = field(b, []byte(who))
	b = field(b, nil) // reserved
	b = field(b, []byte("sha512"))
	b = field(b, field(field(nil, []byte("ssh-ed25519")), ed25519.Sign(s.key, over)))
	return pem.EncodeToMemory(&pem.Block{Type: "SSH SIGNATURE", Bytes: b})
}
