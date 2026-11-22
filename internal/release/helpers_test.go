package release

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"testing"
	"time"
)

// What the tests sign for, and when: a day inside the year of every certificate made below.
const who = "release@usr.games"

var (
	day0 = time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	noon = day0.Add(36 * time.Hour)
	year = uint64(day0.AddDate(1, 0, 0).Unix())
)

// key is a key pair made for a test. It signs as ssh-keygen does, so that a test can make any
// signature it wants refused without a real key. openssh_test.go holds this file's idea of the
// formats to the real ssh-keygen's.
type key struct {
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
}

func newKey(t *testing.T) key {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return key{pub, priv}
}

// wire returns the public key in the form SSH writes it.
func (k key) wire() []byte { return field(field(nil, keyKind), string(k.pub)) }

// signs returns a list of signers with this key as its one root.
func (k key) signs(t *testing.T) Signers {
	t.Helper()
	s, err := ParseSigners([]byte(who + ` cert-authority,namespaces="` + who + `" ` + keyKind + " " + base64.StdEncoding.EncodeToString(k.wire()) + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// cert is what a test wants a certificate to say. The zero value is a good one for the studio's
// releases: a user's certificate for who, from day0 for a year.
type cert struct {
	names      []string // nil means who alone
	after      uint64   // 0 means day0
	before     uint64   // 0 means a year after day0
	host       bool
	conditions string
	extensions string
	extra      string // bytes after the signer's signature, which nobody signed
}

// vouch returns a certificate in which k vouches for the key of.
func (k key) vouch(of key, c cert) []byte {
	if c.names == nil {
		c.names = []string{who}
	}
	if c.after == 0 {
		c.after = uint64(day0.Unix())
	}
	if c.before == 0 {
		c.before = year
	}
	var names []byte
	for _, n := range c.names {
		names = field(names, n)
	}
	kind := uint32(1)
	if c.host {
		kind = 2
	}
	b := field(nil, certKind)
	b = field(b, "a nonce, which no reader looks at")
	b = field(b, string(of.pub))
	b = binary.BigEndian.AppendUint64(b, 1)
	b = binary.BigEndian.AppendUint32(b, kind)
	b = field(b, "made for a test")
	b = field(b, string(names))
	b = binary.BigEndian.AppendUint64(b, c.after)
	b = binary.BigEndian.AppendUint64(b, c.before)
	b = field(b, c.conditions)
	b = field(b, c.extensions)
	b = field(b, "")
	b = field(b, string(k.wire()))
	return append(field(b, string(field(field(nil, keyKind), string(ed25519.Sign(k.priv, b))))), c.extra...)
}

// envelope is what a test wants a signature to say. The zero value is what ssh-keygen writes for
// a release: version 1, the namespace who, SHA-512, nothing reserved.
type shape struct {
	version   uint32 // 0 means 1
	namespace string // "" means who
	hash      string // "" means sha512
	reserved  string
	extra     string // bytes after the end
}

// sign returns the armored signature of message by k, which presents itself as signer: its
// certificate, or its bare key.
func (k key) sign(message, signer []byte, s shape) []byte {
	if s.version == 0 {
		s.version = 1
	}
	if s.namespace == "" {
		s.namespace = who
	}
	if s.hash == "" {
		s.hash = "sha512"
	}
	var digest []byte
	if s.hash == "sha256" {
		d := sha256.Sum256(message)
		digest = d[:]
	} else {
		d := sha512.Sum512(message)
		digest = d[:]
	}
	b := binary.BigEndian.AppendUint32([]byte("SSHSIG"), s.version)
	b = field(b, string(signer))
	b = field(b, s.namespace)
	b = field(b, s.reserved)
	b = field(b, s.hash)
	b = field(b, string(field(field(nil, keyKind), string(ed25519.Sign(k.priv, signedOver(s.namespace, s.hash, digest))))))
	b = append(b, s.extra...)
	return pem.EncodeToMemory(&pem.Block{Type: "SSH SIGNATURE", Bytes: b})
}

// studio returns a root, a release key that the root vouches for, and the list that trusts the
// root: the studio's arrangement, with keys made for the test.
func studio(t *testing.T) (root, release key, signers Signers) {
	t.Helper()
	root, release = newKey(t), newKey(t)
	return root, release, root.signs(t)
}
