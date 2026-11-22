package release

import (
	"crypto/ed25519"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
)

// The two kinds this package reads. It reads no other: the studio's root and release keys are
// Ed25519, and a release is signed with a certificate.
const (
	keyKind  = "ssh-ed25519"
	certKind = "ssh-ed25519-cert-v01@openssh.com"
)

// errShort is what a reader keeps once a field ran past the end of what it was given.
var errShort = errors.New("cut short")

// reader walks what SSH writes one after another in a key, a certificate or a signature: a text
// or a blob as four bytes of length and then that many bytes, a number as eight bytes or four.
// It never reads past the end: a field that would sets err, and every read after gives nothing.
type reader struct {
	rest []byte
	err  error
}

func (r *reader) take(n uint64) []byte {
	if r.err != nil || n > uint64(len(r.rest)) {
		r.err = errShort
		return nil
	}
	b := r.rest[:n]
	r.rest = r.rest[n:]
	return b
}

func (r *reader) num32() uint32 {
	b := r.take(4)
	if b == nil {
		return 0
	}
	return binary.BigEndian.Uint32(b)
}

func (r *reader) num64() uint64 {
	b := r.take(8)
	if b == nil {
		return 0
	}
	return binary.BigEndian.Uint64(b)
}

func (r *reader) blob() []byte { return r.take(uint64(r.num32())) }
func (r *reader) text() string { return string(r.blob()) }

// end reports what is wrong with how the reading went: a field cut short, or bytes left over.
func (r *reader) end() error {
	switch {
	case r.err != nil:
		return r.err
	case len(r.rest) != 0:
		return fmt.Errorf("%d bytes left over", len(r.rest))
	}
	return nil
}

// publicKey reads an Ed25519 public key in the form SSH writes it: its kind, then its 32 bytes.
func publicKey(b []byte) (ed25519.PublicKey, error) {
	r := &reader{rest: b}
	kind, key := r.text(), r.blob()
	if err := r.end(); err != nil {
		return nil, err
	}
	if kind != keyKind || len(key) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("not an %s key", keyKind)
	}
	return ed25519.PublicKey(key), nil
}

// certificate is a key that another key vouches for: for whom, from when, and until when.
type certificate struct {
	key           ed25519.PublicKey // the key vouched for
	user          bool              // a user's certificate, the kind that signs files, not a host's
	names         []string          // the identities it is good for
	after, before uint64            // seconds since 1970: good from after, and no longer at before
	conditions    []byte            // critical options: what a reader must understand or refuse
	signer        ed25519.PublicKey // the key that vouches
	signed        []byte            // what the signer signed: everything before its signature
	signature     []byte
}

// parseCertificate reads an Ed25519 certificate in the form SSH writes it. It checks nothing but
// the shape: whether the signer is trusted and the signature good is the caller's to decide.
func parseCertificate(b []byte) (*certificate, error) {
	r := &reader{rest: b}
	if kind := r.text(); r.err == nil && kind != certKind {
		return nil, fmt.Errorf("not an %s", certKind)
	}
	r.blob() // a nonce
	c := &certificate{key: ed25519.PublicKey(r.blob())}
	r.num64() // the serial number
	c.user = r.num32() == 1
	r.blob() // the certificate's own name
	names := &reader{rest: r.blob()}
	for names.err == nil && len(names.rest) > 0 {
		c.names = append(c.names, names.text())
	}
	c.after, c.before = r.num64(), r.num64()
	c.conditions = r.blob()
	r.blob() // extensions: leave a reader may take or leave
	r.blob() // reserved
	signer := r.blob()
	c.signed = b[:len(b)-len(r.rest)]
	sig := &reader{rest: r.blob()}
	kind := sig.text()
	c.signature = sig.blob()
	for _, err := range []error{r.end(), names.end(), sig.end()} {
		if err != nil {
			return nil, err
		}
	}
	var err error
	if c.signer, err = publicKey(signer); err != nil {
		return nil, fmt.Errorf("its signer: %w", err)
	}
	if len(c.key) != ed25519.PublicKeySize || kind != keyKind || len(c.signature) != ed25519.SignatureSize {
		return nil, fmt.Errorf("not an Ed25519 certificate signed by an Ed25519 key")
	}
	return c, nil
}

// envelope is a signature of a file as `ssh-keygen -Y sign` writes it: who signed, for what
// purpose, with which hash, and the signature itself.
type envelope struct {
	key       []byte // the signer as written: a key, or for a release a certificate
	namespace string // the purpose the signature was made for
	hash      string // sha512 or sha256
	signature []byte
}

// parseEnvelope reads an armored SSH signature. It checks nothing but the shape.
func parseEnvelope(armored []byte) (*envelope, error) {
	block, rest := pem.Decode(armored)
	if block == nil || block.Type != "SSH SIGNATURE" || len(block.Headers) != 0 || strings.TrimSpace(string(rest)) != "" {
		return nil, errors.New("not one armored SSH signature")
	}
	r := &reader{rest: block.Bytes}
	if string(r.take(6)) != "SSHSIG" || r.num32() != 1 {
		return nil, errors.New("not version 1 of an SSH signature")
	}
	e := &envelope{key: r.blob(), namespace: r.text()}
	reserved := r.blob()
	e.hash = r.text()
	sig := &reader{rest: r.blob()}
	kind := sig.text()
	e.signature = sig.blob()
	for _, err := range []error{r.end(), sig.end()} {
		if err != nil {
			return nil, err
		}
	}
	// OpenSSH signs over an empty reserved field whatever the envelope holds, so one that is
	// not empty would be bytes nobody signed.
	if len(reserved) != 0 || kind != keyKind || len(e.signature) != ed25519.SignatureSize {
		return nil, errors.New("not an Ed25519 SSH signature")
	}
	return e, nil
}

// field writes one text or blob as SSH does: four bytes of length, then the bytes.
func field(b []byte, s string) []byte {
	b = binary.BigEndian.AppendUint32(b, uint32(len(s)))
	return append(b, s...)
}

// signedOver returns the bytes an SSH signature of a file really covers: not the file but a
// short note naming the purpose, the hash, and the file's digest.
func signedOver(namespace, hash string, digest []byte) []byte {
	b := []byte("SSHSIG")
	b = field(b, namespace)
	b = field(b, "") // reserved
	b = field(b, hash)
	return field(b, string(digest))
}
