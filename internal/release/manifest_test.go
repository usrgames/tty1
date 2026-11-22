package release

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
	"time"
)

func TestARealManifestOpensAsWhatItIsAndAsNothingElse(t *testing.T) {
	manifest, sig := real(t)
	now := time.Date(2027, 3, 1, 12, 0, 0, 0, time.UTC)
	m, err := Studio().Open(manifest, sig, "relay", "v0.1.0-rc.1", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Files) != 4 {
		t.Errorf("wanted its four archives, got %d files", len(m.Files))
	}
	if f, ok := m.For("darwin", "arm64"); !ok || f.Name != "relay_darwin_arm64.tar.gz" {
		t.Errorf("wanted relay_darwin_arm64.tar.gz for a Mac, got %q, %v", f.Name, ok)
	}
	if _, ok := m.For("windows", "amd64"); ok {
		t.Error("wanted no archive for a system it has none for")
	}
	if _, ok := m.For("", ""); ok {
		t.Error("wanted no archive when no system is named")
	}
	// The studio did sign this manifest. It is still not the one asked for.
	for _, tt := range []struct{ name, version string }{
		{"rustwake", "v0.1.0-rc.1"},
		{"relay", "v0.1.0"},
		{"relay", ""},
		{"", ""},
	} {
		if _, err := Studio().Open(manifest, sig, tt.name, tt.version, now); !errors.Is(err, ErrOther) {
			t.Errorf("asked for %q %q: wanted %v, got %v", tt.name, tt.version, ErrOther, err)
		}
	}
	// As the newest release of relay, it is whatever version it says. Of anything else, nothing.
	if m, err := Studio().Newest(manifest, sig, "relay", now); err != nil || m.Version != "v0.1.0-rc.1" {
		t.Errorf("as the newest relay: wanted v0.1.0-rc.1, got %q and %v", m.Version, err)
	}
	if _, err := Studio().Newest(manifest, sig, "rustwake", now); !errors.Is(err, ErrOther) {
		t.Errorf("as the newest rustwake: wanted %v, got %v", ErrOther, err)
	}
	if _, err := Studio().Newest(manifest, nil, "relay", now); !errors.Is(err, ErrMalformed) {
		t.Errorf("as the newest relay, with no signature: wanted %v, got %v", ErrMalformed, err)
	}
	// And unsigned, it is nothing.
	if _, err := Studio().Open(manifest, nil, "relay", "v0.1.0-rc.1", now); !errors.Is(err, ErrMalformed) {
		t.Errorf("with no signature: wanted %v, got %v", ErrMalformed, err)
	}
}

// A manifest is read only once its signature is good, and then it is still held to what the
// release workflow writes: a signed manifest is trusted to be the studio's, not to be well made.
func TestASignedManifestIsStillHeldToItsShape(t *testing.T) {
	root, release, signers := studio(t)
	good := root.vouch(release, cert{})
	sum := strings.Repeat("ab", sha256.Size)
	file := func(fields string) string {
		return `{"schema": 1, "name": "hello", "version": "v1.0.0", "files": [` + fields + `]}`
	}
	entry := `{"name": "hello_linux_amd64.tar.gz", "size": 10, "sha256": "` + sum + `", "os": "linux", "arch": "amd64"}`

	for _, tt := range []struct {
		what, manifest string
		want           error
	}{
		{"a release", file(entry), nil},
		{"a release with no files", file(""), nil},
		{"a file that runs on nothing in particular", file(`{"name": "hello.6.html", "size": 0, "sha256": "` + sum + `"}`), nil},
		{"a field a later workflow might add", `{"schema": 1, "name": "hello", "version": "v1.0.0", "files": [], "built": "today"}`, nil},

		{"a manifest of a kind not known yet", `{"schema": 2, "name": "hello", "version": "v1.0.0", "files": []}`, ErrManifest},
		{"a manifest of no kind", `{"name": "hello", "version": "v1.0.0", "files": []}`, ErrManifest},
		{"a manifest of another version", `{"schema": 1, "name": "hello", "version": "v0.9.0", "files": []}`, ErrOther},
		{"a manifest of another program", `{"schema": 1, "name": "goodbye", "version": "v1.0.0", "files": []}`, ErrOther},
		{"text that is not JSON", `hello`, ErrManifest},
		{"JSON with something after it", file(entry) + ` {}`, ErrManifest},
		{"a size that is text", file(`{"name": "a", "size": "10", "sha256": "` + sum + `"}`), ErrManifest},
		{"a file in a folder", file(`{"name": "bin/hello", "size": 1, "sha256": "` + sum + `"}`), ErrManifest},
		{"a file one folder up", file(`{"name": "../hello", "size": 1, "sha256": "` + sum + `"}`), ErrManifest},
		{"a file named as the folder above", file(`{"name": "..", "size": 1, "sha256": "` + sum + `"}`), ErrManifest},
		{"a hidden file", file(`{"name": ".profile", "size": 1, "sha256": "` + sum + `"}`), ErrManifest},
		{"a file with a Windows path", file(`{"name": "bin\\hello", "size": 1, "sha256": "` + sum + `"}`), ErrManifest},
		{"a file with a line break in its name", file(`{"name": "a\nb", "size": 1, "sha256": "` + sum + `"}`), ErrManifest},
		{"a file with no name", file(`{"size": 1, "sha256": "` + sum + `"}`), ErrManifest},
		{"the same file twice", file(entry + "," + entry), ErrManifest},
		{"a file smaller than nothing", file(`{"name": "a", "size": -1, "sha256": "` + sum + `"}`), ErrManifest},
		{"a file larger than any disk", file(`{"name": "a", "size": 9223372036854775807, "sha256": "` + sum + `"}`), ErrManifest},
		{"a file with no hash", file(`{"name": "a", "size": 1}`), ErrManifest},
		{"a hash cut short", file(`{"name": "a", "size": 1, "sha256": "` + sum[:62] + `"}`), ErrManifest},
		{"a hash in capitals", file(`{"name": "a", "size": 1, "sha256": "` + strings.ToUpper(sum) + `"}`), ErrManifest},
		{"a system with no processor", file(`{"name": "a", "size": 1, "sha256": "` + sum + `", "os": "linux"}`), ErrManifest},
	} {
		manifest := []byte(tt.manifest)
		m, err := signers.Open(manifest, release.sign(manifest, good, shape{}), "hello", "v1.0.0", noon)
		if !errors.Is(err, tt.want) {
			t.Errorf("%s: wanted %v, got %v", tt.what, tt.want, err)
		}
		if err != nil && (m.Name != "" || m.Files != nil) {
			t.Errorf("%s: wanted nothing of a refused manifest handed back, got %+v", tt.what, m)
		}
		// The newest release is held to the same shape, whatever its version.
		_, err = signers.Newest(manifest, release.sign(manifest, good, shape{}), "hello", noon)
		if want := tt.want; tt.what == "a manifest of another version" {
			if err != nil {
				t.Errorf("%s, as the newest: wanted it opened, got %v", tt.what, err)
			}
		} else if !errors.Is(err, want) {
			t.Errorf("%s, as the newest: wanted %v, got %v", tt.what, want, err)
		}
	}
}

// The newest release has to say which version it is: that is the whole of what is asked of it.
func TestTheNewestReleaseMustNameItsVersion(t *testing.T) {
	root, release, signers := studio(t)
	manifest := []byte(`{"schema": 1, "name": "hello", "version": "", "files": []}`)
	sig := release.sign(manifest, root.vouch(release, cert{}), shape{})
	if _, err := signers.Newest(manifest, sig, "hello", noon); !errors.Is(err, ErrManifest) {
		t.Errorf("wanted %v, got %v", ErrManifest, err)
	}
}

func TestAFileIsHeldToTheManifest(t *testing.T) {
	root, release, signers := studio(t)
	content := []byte("the program, packed\n")
	sum := sha256.Sum256(content)
	manifest := []byte(`{"schema": 1, "name": "hello", "version": "v1.0.0", "files": [
		{"name": "hello.tar.gz", "size": ` + "20" + `, "sha256": "` + hex.EncodeToString(sum[:]) + `"},
		{"name": "empty", "size": 0, "sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"}]}`)
	m, err := signers.Open(manifest, release.sign(manifest, root.vouch(release, cert{}), shape{}), "hello", "v1.0.0", noon)
	if err != nil {
		t.Fatal(err)
	}
	// Neither file says what it runs on, so neither is the archive for a system left unnamed.
	if f, ok := m.For("", ""); ok {
		t.Errorf("wanted no archive when no system is named, got %s", f.Name)
	}
	other := bytes.Clone(content)
	other[3] ^= 1

	for _, tt := range []struct {
		what, name string
		file       io.Reader
		want       error
	}{
		{"the file", "hello.tar.gz", bytes.NewReader(content), nil},
		{"the file, a byte at a time", "hello.tar.gz", iotest.OneByteReader(bytes.NewReader(content)), nil},
		{"an empty file that should be empty", "empty", strings.NewReader(""), nil},
		{"a file the manifest does not name", "other.tar.gz", bytes.NewReader(content), ErrUnnamed},
		{"the manifest itself, which names itself nowhere", "manifest.json", bytes.NewReader(manifest), ErrUnnamed},
		{"the file with one byte changed", "hello.tar.gz", bytes.NewReader(other), ErrFile},
		{"the file cut short", "hello.tar.gz", bytes.NewReader(content[:19]), ErrFile},
		{"the file with a byte added", "hello.tar.gz", bytes.NewReader(append(bytes.Clone(content), 'x')), ErrFile},
		{"something where an empty file should be", "empty", strings.NewReader("x"), ErrFile},
		{"a file that cannot be read to its end", "hello.tar.gz", iotest.ErrReader(io.ErrUnexpectedEOF), io.ErrUnexpectedEOF},
	} {
		if err := m.Check(tt.name, tt.file); !errors.Is(err, tt.want) {
			t.Errorf("%s: wanted %v, got %v", tt.what, tt.want, err)
		}
	}

	// A file that never ends is given up on one byte past what the manifest promises.
	endless := &counting{r: zeros{}}
	if err := m.Check("hello.tar.gz", endless); !errors.Is(err, ErrFile) {
		t.Errorf("a file that never ends: wanted %v, got %v", ErrFile, err)
	}
	if endless.n != int64(len(content))+1 {
		t.Errorf("wanted %d bytes read of a file that never ends, got %d", len(content)+1, endless.n)
	}
}

// zeros is a file that never ends.
type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

// counting counts what is read through it.
type counting struct {
	r io.Reader
	n int64
}

func (c *counting) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
