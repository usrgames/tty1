package release

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// Why a manifest or a file is refused, once the signature is good.
var (
	ErrManifest = errors.New("the manifest is not one this can use")
	ErrOther    = errors.New("the manifest is for something else")
	ErrUnnamed  = errors.New("the manifest does not name this file")
	ErrFile     = errors.New("the file is not the one the manifest names")
)

// maxSize is larger than any file of a release could be, and small enough to add to safely.
const maxSize = 1 << 40

// Manifest is the list of a release's files: manifest.json, as the release workflow writes it.
type Manifest struct {
	Schema  int    `json:"schema"`
	Name    string `json:"name"`    // the program
	Version string `json:"version"` // the tag, such as v0.1.0
	Files   []File `json:"files"`
}

// File is one file of a release. An archive of the program also says what it runs on.
type File struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	OS     string `json:"os,omitempty"`
	Arch   string `json:"arch,omitempty"`
}

// Open is the one way to a manifest. It checks the signature of the bytes first, then reads
// them, then checks that this is the manifest asked for: a manifest the studio did sign, of
// another program or of an older release, is refused like any other that is not wanted.
func (s Signers) Open(manifest, sig []byte, name, version string, now time.Time) (Manifest, error) {
	var m Manifest
	if err := s.Verify(manifest, sig, now); err != nil {
		return m, err
	}
	if err := json.Unmarshal(manifest, &m); err != nil {
		return Manifest{}, fmt.Errorf("%w: %v", ErrManifest, err)
	}
	if m.Schema != 1 {
		return Manifest{}, fmt.Errorf("%w: it is of kind %d, and this knows kind 1: a newer tty1 may read it", ErrManifest, m.Schema)
	}
	if m.Name != name || m.Version != version {
		return Manifest{}, fmt.Errorf("%w: it is %s %s, and %s %s was asked for", ErrOther, m.Name, m.Version, name, version)
	}
	seen := map[string]bool{}
	for _, f := range m.Files {
		switch {
		case !plain(f.Name):
			return Manifest{}, fmt.Errorf("%w: %q is not a plain file name", ErrManifest, f.Name)
		case seen[f.Name]:
			return Manifest{}, fmt.Errorf("%w: it names %s twice", ErrManifest, f.Name)
		case f.Size < 0 || f.Size > maxSize:
			return Manifest{}, fmt.Errorf("%w: %s has the size %d", ErrManifest, f.Name, f.Size)
		case len(f.SHA256) != sha256.Size*2 || strings.Trim(f.SHA256, "0123456789abcdef") != "":
			return Manifest{}, fmt.Errorf("%w: %s has no SHA-256", ErrManifest, f.Name)
		case (f.OS == "") != (f.Arch == ""):
			return Manifest{}, fmt.Errorf("%w: %s names a system or a processor but not both", ErrManifest, f.Name)
		}
		seen[f.Name] = true
	}
	return m, nil
}

// plain reports whether a name is a file's name and nothing more: no folder, no way up, nothing
// hidden. A manifest is signed, but a name from it still ends up in a path on a player's disk.
func plain(name string) bool {
	if name == "" || name[0] == '.' || len(name) > 200 {
		return false
	}
	for _, r := range name {
		if r == '/' || r == '\\' || r < ' ' || r == 0x7f {
			return false
		}
	}
	return true
}

// For returns the archive of the program for a system and a processor, such as darwin and arm64.
func (m Manifest) For(os, arch string) (File, bool) {
	for _, f := range m.Files {
		if os != "" && f.OS == os && f.Arch == arch {
			return f, true
		}
	}
	return File{}, false
}

// Check reads r to its end and reports whether it is the file the manifest names by name: the
// same size and the same SHA-256. It reads at most one byte more than the manifest promises, so
// a file that never ends cannot keep it reading.
func (m Manifest) Check(name string, r io.Reader) error {
	for _, f := range m.Files {
		if f.Name != name {
			continue
		}
		h := sha256.New()
		n, err := io.Copy(h, io.LimitReader(r, f.Size+1))
		if err != nil {
			return fmt.Errorf("reading %s: %w", name, err)
		}
		if n != f.Size {
			return fmt.Errorf("%w: %s should be %d bytes and is not", ErrFile, name, f.Size)
		}
		if hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
			return fmt.Errorf("%w: %s has another SHA-256", ErrFile, name)
		}
		return nil
	}
	return fmt.Errorf("%w: %s", ErrUnnamed, name)
}
