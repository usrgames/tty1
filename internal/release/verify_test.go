package release

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The real thing: the manifest of usrgames/arcade's pre-release v0.1.0-rc.1 and its signature,
// the first made with the studio's release key. They never change.
func real(t testing.TB) (manifest, sig []byte) {
	t.Helper()
	dir := filepath.Join("testdata", "arcade-v0.1.0-rc.1")
	manifest, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	sig, err = os.ReadFile(filepath.Join(dir, "manifest.json.sig"))
	if err != nil {
		t.Fatal(err)
	}
	return manifest, sig
}

// The one key tty1 trusts is the studio's root. Changing the built-in list has to be a decision
// made in two places, here and there, never a slip in one.
func TestTheBuiltInListNamesTheStudiosRoot(t *testing.T) {
	s := Studio()
	if s.identity != who || s.namespace != who || len(s.roots) != 1 {
		t.Fatalf("wanted one root, for %s and for releases only, got %d for %q in the namespace %q", who, len(s.roots), s.identity, s.namespace)
	}
	sum := sha256.Sum256(field(field(nil, keyKind), string(s.roots[0])))
	if got, want := "SHA256:"+base64.RawStdEncoding.EncodeToString(sum[:]), "SHA256:ftIfXrwCCHVZunpdo7y85JGaI1Gl1TdoP4RDicQhXSI"; got != want {
		t.Errorf("wanted the root with the fingerprint %s, as KEYS.md of usrgames/usr.games gives it, got %s", want, got)
	}
}

func TestARealReleaseIsAcceptedWhileItsCertificateIsGood(t *testing.T) {
	manifest, sig := real(t)
	e, err := parseEnvelope(sig)
	if err != nil {
		t.Fatal(err)
	}
	c, err := parseCertificate(e.key)
	if err != nil {
		t.Fatal(err)
	}
	at := func(seconds uint64) time.Time { return time.Unix(int64(seconds), 0) }
	for _, tt := range []struct {
		what string
		now  time.Time
		want error
	}{
		{"a day in its year", time.Date(2027, 3, 1, 12, 0, 0, 0, time.UTC), nil},
		{"the second it starts", at(c.after), nil},
		{"the second before it starts", at(c.after - 1), ErrNotYet},
		{"its last second", at(c.before - 1), nil},
		{"the second it ends", at(c.before), ErrEnded},
		{"a year too late", time.Date(2028, 11, 22, 0, 0, 0, 0, time.UTC), ErrEnded},
		{"a clock at the start of time", time.Unix(0, 0), ErrNotYet},
		{"a clock before the start of time", time.Unix(-1, 0), ErrNotYet},
	} {
		if err := Studio().Verify(manifest, sig, tt.now); !errors.Is(err, tt.want) {
			t.Errorf("%s: wanted %v, got %v", tt.what, tt.want, err)
		}
	}
}

// No one byte of a real signature can be changed, dropped or added and the result still pass.
// A reader that crashed on any of them would fail here too.
func TestNoAlteredCopyOfARealSignatureIsAccepted(t *testing.T) {
	manifest, sig := real(t)
	now := time.Date(2027, 3, 1, 12, 0, 0, 0, time.UTC)
	block, _ := pem.Decode(sig)
	armor := func(b []byte) []byte { return pem.EncodeToMemory(&pem.Block{Type: block.Type, Bytes: b}) }
	if err := Studio().Verify(manifest, armor(block.Bytes), now); err != nil {
		t.Fatalf("the signature, armored again and unchanged: %v", err)
	}
	// Nothing may stand before the armor, not even an empty line: ssh-keygen refuses that too.
	for _, before := range []string{"\n", " ", "a line before it\n", "-----BEGIN NOTHING-----\n-----END NOTHING-----\n"} {
		if err := Studio().Verify(manifest, append([]byte(before), sig...), now); !errors.Is(err, ErrMalformed) {
			t.Fatalf("the signature with %q before it: wanted %v, got %v", before, ErrMalformed, err)
		}
	}
	for i := range block.Bytes {
		for _, flip := range []byte{0x01, 0x80, 0xff} {
			changed := bytes.Clone(block.Bytes)
			changed[i] ^= flip
			if Studio().Verify(manifest, armor(changed), now) == nil {
				t.Fatalf("accepted the signature with byte %d changed by %#x", i, flip)
			}
		}
		if Studio().Verify(manifest, armor(block.Bytes[:i]), now) == nil {
			t.Fatalf("accepted the signature cut off after %d bytes", i)
		}
	}
	if Studio().Verify(manifest, armor(append(bytes.Clone(block.Bytes), 0)), now) == nil {
		t.Error("accepted the signature with a byte added")
	}
	// The same good bytes under another label, or with a header, are not an SSH signature.
	for _, other := range []*pem.Block{
		{Type: "PGP SIGNATURE", Bytes: block.Bytes},
		{Type: "SSH SIGNATURE", Headers: map[string]string{"Comment": "x"}, Bytes: block.Bytes},
	} {
		if err := Studio().Verify(manifest, pem.EncodeToMemory(other), now); !errors.Is(err, ErrMalformed) {
			t.Errorf("the signature armored as %q with %d headers: wanted %v, got %v", other.Type, len(other.Headers), ErrMalformed, err)
		}
	}
	for i := range manifest {
		changed := bytes.Clone(manifest)
		changed[i] ^= 0x01
		if err := Studio().Verify(changed, sig, now); !errors.Is(err, ErrSignature) {
			t.Fatalf("the manifest with byte %d changed: wanted %v, got %v", i, ErrSignature, err)
		}
	}
}

// Each reason a signature is refused, with keys made for the test, so that each can be made to
// happen alone. The signature is otherwise exactly what a release's is.
func TestASignatureIsRefusedForItsOwnReason(t *testing.T) {
	root, release, signers := studio(t)
	stranger, other := newKey(t), newKey(t)
	message := []byte("the manifest of a release\n")
	good := root.vouch(release, cert{})

	for _, tt := range []struct {
		what    string
		message []byte
		sig     []byte
		want    error
	}{
		{"a release", message, release.sign(message, good, shape{}), nil},
		{"a release signed with SHA-256", message, release.sign(message, good, shape{hash: "sha256"}), nil},
		{"a certificate that also grants what a login would use", message, release.sign(message, root.vouch(release, cert{extensions: string(field(field(nil, "permit-pty"), ""))}), shape{}), nil},
		{"a certificate for several identities, the studio's among them", message, release.sign(message, root.vouch(release, cert{names: []string{"someone@usr.games", who}}), shape{}), nil},
		{"a certificate that never ends", message, release.sign(message, root.vouch(release, cert{before: 1<<64 - 1}), shape{}), nil},

		{"another message", []byte("another manifest\n"), release.sign(message, good, shape{}), ErrSignature},
		{"a signature by another key than the certificate's", message, other.sign(message, good, shape{}), ErrSignature},
		{"a signature made for another purpose", message, release.sign(message, good, shape{namespace: "file"}), ErrPurpose},
		{"a bare key, even the release key's", message, release.sign(message, release.wire(), shape{}), ErrNoCertificate},
		{"the root's bare key", message, root.sign(message, root.wire(), shape{}), ErrNoCertificate},
		{"a certificate from a stranger", message, release.sign(message, stranger.vouch(release, cert{}), shape{}), ErrSigner},
		{"a certificate the release key made for another key", message, other.sign(message, release.vouch(other, cert{}), shape{}), ErrSigner},
		{"a certificate for another identity", message, release.sign(message, root.vouch(release, cert{names: []string{"someone@usr.games"}}), shape{}), ErrIdentity},
		{"a certificate for no identity", message, release.sign(message, root.vouch(release, cert{names: []string{}}), shape{}), ErrIdentity},
		{"a host's certificate", message, release.sign(message, root.vouch(release, cert{host: true}), shape{}), ErrIdentity},
		{"a certificate with a condition", message, release.sign(message, root.vouch(release, cert{conditions: string(field(field(nil, "force-command"), "x"))}), shape{}), ErrCondition},
		{"a certificate that starts tomorrow", message, release.sign(message, root.vouch(release, cert{after: uint64(noon.Add(24 * time.Hour).Unix())}), shape{}), ErrNotYet},
		{"a certificate that ended yesterday", message, release.sign(message, root.vouch(release, cert{before: uint64(noon.Add(-24 * time.Hour).Unix())}), shape{}), ErrEnded},

		{"a certificate with bytes after its signature", message, release.sign(message, root.vouch(release, cert{extra: "x"}), shape{}), ErrMalformed},
		{"a signature of version 2", message, release.sign(message, good, shape{version: 2}), ErrMalformed},
		{"a hash this does not know", message, release.sign(message, good, shape{hash: "md5"}), ErrMalformed},
		{"something in the reserved field", message, release.sign(message, good, shape{reserved: "x"}), ErrMalformed},
		{"bytes after the end", message, release.sign(message, good, shape{extra: "x"}), ErrMalformed},
		{"no signature at all", message, nil, ErrMalformed},
		{"text that is not a signature", message, []byte("-----BEGIN SSH SIGNATURE-----\nnot base64\n-----END SSH SIGNATURE-----\n"), ErrMalformed},
		{"two signatures, the second a good one", message, append(stranger.sign(message, stranger.wire(), shape{}), release.sign(message, good, shape{})...), ErrMalformed},
		{"a good signature with another after it", message, append(release.sign(message, good, shape{}), stranger.sign(message, stranger.wire(), shape{})...), ErrMalformed},
		{"armor of another kind", message, pem.EncodeToMemory(&pem.Block{Type: "PGP SIGNATURE", Bytes: []byte("SSHSIG")}), ErrMalformed},
	} {
		if err := signers.Verify(tt.message, tt.sig, noon); !errors.Is(err, tt.want) {
			t.Errorf("%s: wanted %v, got %v", tt.what, tt.want, err)
		}
	}
}

// A list of signers is trust itself, so only the one shape the studio publishes is read. A list
// that says more than that, or says it more loosely, is refused whole.
func TestOnlyTheStudiosKindOfListIsRead(t *testing.T) {
	k := base64.StdEncoding.EncodeToString(newKey(t).wire())
	line := who + ` cert-authority,namespaces="` + who + `" ` + keyKind + " " + k
	for _, tt := range []struct {
		what, list string
		roots      int
	}{
		{"one root", line + "\n", 1},
		{"comments, blank lines and two roots", "# the roots\n\n" + line + "\n" + line + "\n", 2},
		{"a comment after the key, which OpenSSH would allow", line + " a comment\n", 0},
		{"nothing", "", 0},
		{"only a comment", "# nothing\n", 0},
		{"a key trusted by itself, not as a root", who + " " + keyKind + " " + k + "\n", 0},
		{"a root for every purpose", who + " cert-authority " + keyKind + " " + k + "\n", 0},
		{"a root for two purposes", who + ` cert-authority,namespaces="file,` + who + `" ` + keyKind + " " + k + "\n", 0},
		{"a root for any purpose", who + ` cert-authority,namespaces="*" ` + keyKind + " " + k + "\n", 0},
		{"a root for any identity", `*@usr.games cert-authority,namespaces="` + who + `" ` + keyKind + " " + k + "\n", 0},
		{"a root for two identities", who + `,other@usr.games cert-authority,namespaces="` + who + `" ` + keyKind + " " + k + "\n", 0},
		{"a root with a date", who + ` cert-authority,namespaces="` + who + `",valid-before="20300101" ` + keyKind + " " + k + "\n", 0},
		{"two roots for two identities", line + "\nother@usr.games" + line[len(who):] + "\n", 0},
		{"a key of another kind", who + ` cert-authority,namespaces="` + who + `" ssh-rsa ` + k + "\n", 0},
		{"a key that is not base64", who + ` cert-authority,namespaces="` + who + `" ` + keyKind + " !!!\n", 0},
		{"a key cut short", who + ` cert-authority,namespaces="` + who + `" ` + keyKind + " " + k[:20] + "\n", 0},
	} {
		s, err := ParseSigners([]byte(tt.list))
		if (err == nil) != (tt.roots > 0) || len(s.roots) != tt.roots && err == nil {
			t.Errorf("%s: wanted %d roots, got %d and %v", tt.what, tt.roots, len(s.roots), err)
		}
	}
}

// Whatever it is given, the check must say no without crashing, and it says yes to one message
// only: the one the studio signed. `make fuzz` runs this for a while; go test runs the seeds.
func FuzzVerify(f *testing.F) {
	manifest, sig := real(f)
	f.Add(manifest, sig)
	f.Add([]byte{}, []byte{})
	f.Add(manifest, []byte("-----BEGIN SSH SIGNATURE-----\nU1NIU0lH\n-----END SSH SIGNATURE-----\n"))
	now := time.Date(2027, 3, 1, 12, 0, 0, 0, time.UTC)
	f.Fuzz(func(t *testing.T, message, sig []byte) {
		if err := Studio().Verify(message, sig, now); err == nil && !bytes.Equal(message, manifest) {
			t.Fatalf("accepted a message the studio never signed: %q", message)
		}
	})
}
