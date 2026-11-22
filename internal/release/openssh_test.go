package release

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// openssh runs the machine's own ssh-keygen in a folder of keys made for one test.
type openssh struct {
	t   *testing.T
	dir string
}

func (o openssh) run(stdin []byte, args ...string) error {
	cmd := exec.Command("ssh-keygen", args...)
	cmd.Dir = o.dir
	cmd.Stdin = bytes.NewReader(stdin)
	return cmd.Run()
}

func (o openssh) must(args ...string) {
	o.t.Helper()
	cmd := exec.Command("ssh-keygen", args...)
	cmd.Dir = o.dir
	if out, err := cmd.CombinedOutput(); err != nil {
		o.t.Fatalf("ssh-keygen %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func (o openssh) read(name string) []byte {
	o.t.Helper()
	b, err := os.ReadFile(filepath.Join(o.dir, name))
	if err != nil {
		o.t.Fatal(err)
	}
	return b
}

// key makes a key pair with no passphrase.
func (o openssh) key(name string) { o.must("-q", "-t", "ed25519", "-N", "", "-C", name, "-f", name) }

// vouch has the key ca sign a certificate for the key of, with ssh-keygen's own options.
func (o openssh) vouch(ca, of string, options ...string) {
	o.must(append(append([]string{"-q", "-s", ca, "-I", "made for a test"}, options...), of+".pub")...)
}

// sign signs the file message with a key or, given a file ending in -cert.pub, its certificate.
func (o openssh) sign(with, namespace, message string, options ...string) []byte {
	o.t.Helper()
	os.Remove(filepath.Join(o.dir, message+".sig"))
	o.must(append(append([]string{"-q", "-Y", "sign", "-f", with, "-n", namespace}, options...), message)...)
	return o.read(message + ".sig")
}

// The reading of SSH's formats in this package was written here, not taken from a library. This
// is what holds it to the real thing: for every case, on keys made fresh each run, the verdict
// of this machine's ssh-keygen and this package's must be the same. Where this package is
// stricter than OpenSSH on purpose, the case says so.
func TestItsVerdictIsThatOfTheRealSSHKeygen(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("no ssh-keygen on this machine")
	}
	o := openssh{t, t.TempDir()}
	message := []byte("the manifest of a release\n")
	if err := os.WriteFile(filepath.Join(o.dir, "manifest"), message, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"root", "stranger", "release", "plain", "extended", "hash", "foreign", "someone", "nobody", "host", "ended", "notyet", "condition", "chained"} {
		o.key(name)
	}
	good := []string{"-n", who, "-V", "+1h", "-O", "clear"}
	o.vouch("root", "release", good...)
	o.vouch("root", "hash", good...)
	o.vouch("root", "extended", "-n", who, "-V", "+1h")
	o.vouch("stranger", "foreign", good...)
	o.vouch("root", "someone", "-n", "someone@usr.games", "-V", "+1h", "-O", "clear")
	o.vouch("root", "nobody", "-V", "+1h", "-O", "clear")
	o.vouch("root", "host", "-h", "-n", who, "-V", "+1h")
	o.vouch("root", "ended", "-n", who, "-V", "20250101:20250201", "-O", "clear")
	o.vouch("root", "notyet", "-n", who, "-V", "20400101:20410101", "-O", "clear")
	o.vouch("root", "condition", "-n", who, "-V", "+1h", "-O", "clear", "-O", "force-command=/bin/true")
	o.vouch("release", "chained", good...)

	rootKey := strings.Fields(string(o.read("root.pub")))
	list := who + ` cert-authority,namespaces="` + who + `" ` + rootKey[0] + " " + rootKey[1] + "\n"
	if err := os.WriteFile(filepath.Join(o.dir, "allowed_signers"), []byte(list), 0o644); err != nil {
		t.Fatal(err)
	}
	signers, err := ParseSigners([]byte(list))
	if err != nil {
		t.Fatal(err)
	}

	armored := o.sign("release-cert.pub", who, "manifest")
	for _, tt := range []struct {
		what     string
		message  []byte
		sig      []byte
		want     error
		stricter bool // OpenSSH accepts it, and this package refuses it on purpose
	}{
		{"a release", message, o.sign("release-cert.pub", who, "manifest"), nil, false},
		{"a release signed with SHA-256", message, o.sign("hash-cert.pub", who, "manifest", "-O", "hashalg=sha256"), nil, false},
		{"a certificate with ssh-keygen's usual extensions", message, o.sign("extended-cert.pub", who, "manifest"), nil, false},
		{"another message", []byte("another manifest\n"), o.sign("release-cert.pub", who, "manifest"), ErrSignature, false},
		{"a signature made for another purpose", message, o.sign("release-cert.pub", "file", "manifest"), ErrPurpose, false},
		{"a bare key", message, o.sign("plain", who, "manifest"), ErrNoCertificate, false},
		{"the root's bare key", message, o.sign("root", who, "manifest"), ErrNoCertificate, false},
		{"a certificate from a stranger", message, o.sign("foreign-cert.pub", who, "manifest"), ErrSigner, false},
		{"a certificate the release key made for another key", message, o.sign("chained-cert.pub", who, "manifest"), ErrSigner, false},
		{"a certificate for another identity", message, o.sign("someone-cert.pub", who, "manifest"), ErrIdentity, false},
		{"a certificate for no identity", message, o.sign("nobody-cert.pub", who, "manifest"), ErrIdentity, false},
		{"a host's certificate", message, o.sign("host-cert.pub", who, "manifest"), ErrIdentity, false},
		{"a certificate that has ended", message, o.sign("ended-cert.pub", who, "manifest"), ErrEnded, false},
		{"a certificate that is not good yet", message, o.sign("notyet-cert.pub", who, "manifest"), ErrNotYet, false},
		{"a certificate with a condition", message, o.sign("condition-cert.pub", who, "manifest"), ErrCondition, true},
		// The armor, which ssh-keygen wants from the first byte on, with plain line ends.
		{"words before the signature", message, append([]byte("a line before it\n"), armored...), ErrMalformed, false},
		{"an empty line before the signature", message, append([]byte("\n"), armored...), ErrMalformed, false},
		{"words after the signature", message, append(bytes.Clone(armored), "a line after it\n"...), ErrMalformed, true},
	} {
		if err := os.WriteFile(filepath.Join(o.dir, "sig"), tt.sig, 0o644); err != nil {
			t.Fatal(err)
		}
		theirs := o.run(tt.message, "-Y", "verify", "-f", "allowed_signers", "-I", who, "-n", who, "-s", "sig") == nil
		ours := signers.Verify(tt.message, tt.sig, time.Now())
		if !errors.Is(ours, tt.want) {
			t.Errorf("%s: wanted %v, got %v", tt.what, tt.want, ours)
		}
		switch {
		case tt.stricter && !theirs:
			t.Logf("%s: this ssh-keygen refuses it too", tt.what)
		case !tt.stricter && theirs != (ours == nil):
			t.Errorf("%s: ssh-keygen accepts it: %v, and this package: %v (%v)", tt.what, theirs, ours == nil, ours)
		}
	}
}
