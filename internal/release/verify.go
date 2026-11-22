// Package release decides whether a release is the studio's. tty1 downloads programs and runs
// them, so before it unpacks anything it asks this package two things: is the release's manifest
// signed by the studio's release key, and is each file the one the manifest names.
//
// A release is signed as `ssh-keygen -Y sign` signs a file, with a certificate: the studio's root
// key vouches, for a year at a time, for the key that signs releases. So this package holds one
// thing to trust, the root's public key, and it does not change when the release key does.
//
// The reading of SSH's formats is written here, with the standard library alone, and reads only
// the one kind of key and of certificate the studio uses. Nothing outside this repository can
// change what tty1 trusts. The tests hold it to the verdicts of the real ssh-keygen.
package release

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/sha512"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Why a signature is refused. Every refusal from Verify is one of these, with the details added.
var (
	ErrMalformed     = errors.New("the signature is not one this can read")
	ErrPurpose       = errors.New("the signature was made for something other than a release")
	ErrNoCertificate = errors.New("the signature was made with a key that carries no certificate")
	ErrSigner        = errors.New("the certificate is not one the studio's root key signed")
	ErrIdentity      = errors.New("the certificate is not for the studio's releases")
	ErrNotYet        = errors.New("the certificate is not good yet")
	ErrEnded         = errors.New("the certificate has ended")
	ErrCondition     = errors.New("the certificate carries a condition this does not understand")
	ErrSignature     = errors.New("the signature does not match")
)

// Signers is who may sign a release: the root keys, and the one identity and purpose a
// certificate from them is accepted for.
type Signers struct {
	identity  string
	namespace string
	roots     []ed25519.PublicKey
}

// The studio's own list, as published at usr.games/etc/allowed_signers. CI holds this file to
// the site's, and a test holds the key in it to its fingerprint.
//
//go:embed allowed_signers
var studioList []byte

// Studio returns the studio's signers, built into the program.
func Studio() Signers {
	s, err := ParseSigners(studioList)
	if err != nil {
		// The list is part of the program and a test reads it: this cannot happen in a build that passed.
		panic("release: the built-in list of signers: " + err.Error())
	}
	return s
}

// ParseSigners reads a list in the form of OpenSSH's allowed-signers file. tty1 itself uses the
// built-in list, through Studio; this is for tests, which make a studio of their own. It accepts
// the one shape the studio publishes and nothing wider: each line names an identity, makes an Ed25519
// key a certificate authority for it, and limits it to one namespace.
func ParseSigners(list []byte) (Signers, error) {
	var s Signers
	for _, line := range strings.Split(string(list), "\n") {
		if line = strings.TrimSpace(line); line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 4 || f[2] != keyKind {
			return s, fmt.Errorf("wanted `IDENTITY cert-authority,namespaces=\"NAMESPACE\" %s KEY`, got %q", keyKind, line)
		}
		namespace, ok := strings.CutPrefix(f[1], `cert-authority,namespaces="`)
		namespace, closed := strings.CutSuffix(namespace, `"`)
		if !ok || !closed || namespace == "" || strings.ContainsAny(namespace, `",*?!`) || strings.ContainsAny(f[0], `,*?!`) {
			return s, fmt.Errorf("wanted one identity and one namespace, spelled out, got %q", line)
		}
		if s.roots != nil && (s.identity != f[0] || s.namespace != namespace) {
			return s, errors.New("wanted every line to be for the same identity and namespace")
		}
		raw, err := base64.StdEncoding.DecodeString(f[3])
		if err != nil {
			return s, fmt.Errorf("a key that is not base64: %w", err)
		}
		key, err := publicKey(raw)
		if err != nil {
			return s, err
		}
		s.identity, s.namespace, s.roots = f[0], namespace, append(s.roots, key)
	}
	if len(s.roots) == 0 {
		return s, errors.New("wanted at least one root key")
	}
	return s, nil
}

// Verify reports whether sig is these signers' signature of message, as of now. A nil error is
// the only yes.
func (s Signers) Verify(message, sig []byte, now time.Time) error {
	e, err := parseEnvelope(sig)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	if e.namespace != s.namespace {
		return fmt.Errorf("%w: it says %q", ErrPurpose, e.namespace)
	}
	var digest []byte
	switch e.hash {
	case "sha512":
		d := sha512.Sum512(message)
		digest = d[:]
	case "sha256":
		d := sha256.Sum256(message)
		digest = d[:]
	default:
		return fmt.Errorf("%w: the hash %q", ErrMalformed, e.hash)
	}

	switch kind := (&reader{rest: e.key}).text(); kind {
	case certKind:
	case keyKind:
		return ErrNoCertificate
	default:
		return fmt.Errorf("%w: a signer of the kind %q", ErrMalformed, kind)
	}
	c, err := parseCertificate(e.key)
	if err != nil {
		return fmt.Errorf("%w: the certificate: %v", ErrMalformed, err)
	}
	// The root first: nothing a certificate says counts until a root is seen to have signed it.
	if !slices.ContainsFunc(s.roots, func(root ed25519.PublicKey) bool { return bytes.Equal(root, c.signer) }) || !ed25519.Verify(c.signer, c.signed, c.signature) {
		return ErrSigner
	}
	if !c.user || !slices.Contains(c.names, s.identity) {
		return fmt.Errorf("%w: it is for %q", ErrIdentity, c.names)
	}
	if len(c.conditions) != 0 {
		return ErrCondition
	}
	// The dates are judged by this machine's clock, so a refusal says what the clock reads.
	if at := now.Unix(); at < 0 || uint64(at) < c.after {
		return fmt.Errorf("%w: it starts on %s, and this machine's clock reads %s", ErrNotYet, day(c.after), now.UTC().Format(time.DateOnly))
	} else if uint64(at) >= c.before {
		return fmt.Errorf("%w: it ended on %s, and this machine's clock reads %s", ErrEnded, day(c.before), now.UTC().Format(time.DateOnly))
	}
	if !ed25519.Verify(c.key, signedOver(e.namespace, e.hash, digest), e.signature) {
		return ErrSignature
	}
	return nil
}

// day writes a certificate's date, seconds since 1970, as a day. The largest number there is
// means never, and no real date is anywhere near what a time cannot hold.
func day(seconds uint64) string {
	if seconds > 1<<62 {
		return "no day at all"
	}
	return time.Unix(int64(seconds), 0).UTC().Format(time.DateOnly)
}
