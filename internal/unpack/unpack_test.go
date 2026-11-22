package unpack

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// entry is one thing in an archive made for a test.
type entry struct {
	name string
	body string
	kind byte   // 0 means a plain file
	mode int64  // 0 means 0644
	link string // for a link, what it points at
}

// archive returns a gzipped tar of the entries, as a release's is made.
func archive(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Typeflag: e.kind, Mode: e.mode, Linkname: e.link, Size: int64(len(e.body))}
		if e.kind == 0 {
			h.Typeflag = tar.TypeReg
		}
		if e.mode == 0 {
			h.Mode = 0o644
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// A release's archive as the release workflow packs it: the folder itself, the program, the licence.
func TestAReleasesArchiveIsUnpacked(t *testing.T) {
	dir := t.TempDir()
	names, err := Into(dir, bytes.NewReader(archive(t,
		entry{name: "./", kind: tar.TypeDir, mode: 0o700},
		entry{name: "./LICENSE", body: "MIT\n"},
		entry{name: "./hello", body: "#!/bin/sh\necho hello\n", mode: 0o755},
	)))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(names, []string{"LICENSE", "hello"}) {
		t.Errorf("wanted LICENSE and hello, got %v", names)
	}
	for name, want := range map[string]os.FileMode{"LICENSE": 0o644, "hello": 0o755} {
		info, err := os.Lstat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != want {
			t.Errorf("%s: wanted a plain file with mode %o, got %v", name, want, info.Mode())
		}
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "hello")); string(got) != "#!/bin/sh\necho hello\n" {
		t.Errorf("wanted the program as packed, got %q", got)
	}
}

// GNU tar, which packs the real releases, fills its last record with zeros after the end mark.
// That padding is part of a good archive, and is all that may follow the end.
func TestTheZerosTarPadsWithAreNotSomethingExtra(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "./hello", Typeflag: tar.TypeReg, Mode: 0o755, Size: 5}); err != nil {
		t.Fatal(err)
	}
	tw.Write([]byte("hello"))
	tw.Close()
	gz.Write(make([]byte, 17*512)) // the rest of a record of twenty blocks
	gz.Close()
	if names, err := Into(t.TempDir(), bytes.NewReader(buf.Bytes())); err != nil || len(names) != 1 {
		t.Errorf("wanted hello unpacked from a padded archive, got %v and %v", names, err)
	}
}

// Whatever is not a handful of plain files side by side is refused, and nothing is written
// outside the folder that was given.
func TestWhatIsNotAReleasesArchiveIsRefused(t *testing.T) {
	good := entry{name: "hello", body: "x"}
	for _, tt := range []struct {
		what string
		data []byte
	}{
		{"a file one folder up", archive(t, entry{name: "../escaped", body: "x"})},
		{"a file reached through the folder", archive(t, entry{name: "./../escaped", body: "x"})},
		{"a file at the top of the disk", archive(t, entry{name: "/tmp/escaped", body: "x"})},
		{"a file in a folder", archive(t, entry{name: "bin/hello", body: "x"})},
		{"a folder", archive(t, good, entry{name: "bin/", kind: tar.TypeDir})},
		{"a hidden file", archive(t, entry{name: ".profile", body: "x"})},
		{"a file with a Windows path", archive(t, entry{name: `bin\hello`, body: "x"})},
		{"a file with a line break in its name", archive(t, entry{name: "a\nb", body: "x"})},
		{"a link to somewhere else", archive(t, good, entry{name: "escape", kind: tar.TypeSymlink, link: "/etc"})},
		{"a second name for a file", archive(t, good, entry{name: "again", kind: tar.TypeLink, link: "hello"})},
		{"a device", archive(t, good, entry{name: "tty", kind: tar.TypeChar})},
		{"a pipe", archive(t, good, entry{name: "pipe", kind: tar.TypeFifo})},
		{"the same file twice", archive(t, good, good)},
		{"no file at all", archive(t)},
		{"only the folder itself", archive(t, entry{name: "./", kind: tar.TypeDir})},
		{"the folder itself twice", archive(t, entry{name: "./", kind: tar.TypeDir}, entry{name: "./", kind: tar.TypeDir}, good)},
		{"the folder itself again after a file", archive(t, entry{name: "./", kind: tar.TypeDir}, good, entry{name: ".", kind: tar.TypeDir})},
		{"something that is not gzipped", []byte("plain text")},
		{"gzip of something that is not a tar", func() []byte {
			var b bytes.Buffer
			gz := gzip.NewWriter(&b)
			gz.Write([]byte(strings.Repeat("not a tar ", 100)))
			gz.Close()
			return b.Bytes()
		}()},
		{"nothing", nil},
	} {
		dir := t.TempDir()
		inside := filepath.Join(dir, "inside")
		if err := os.Mkdir(inside, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := Into(inside, bytes.NewReader(tt.data)); !errors.Is(err, ErrArchive) {
			t.Errorf("%s: wanted %v, got %v", tt.what, ErrArchive, err)
		}
		if left, _ := os.ReadDir(dir); len(left) != 1 {
			t.Errorf("%s: wanted nothing written beside the folder it was given, got %d entries there", tt.what, len(left))
		}
		if _, err := os.Lstat("/tmp/escaped"); err == nil {
			t.Fatalf("%s: wrote /tmp/escaped", tt.what)
		}
	}
}

// An archive is cut off wherever a download is: none of its beginnings may pass for the whole.
func TestAnArchiveCutShortIsRefused(t *testing.T) {
	whole := archive(t, entry{name: "LICENSE", body: "MIT\n"}, entry{name: "hello", body: strings.Repeat("x", 4000), mode: 0o755})
	for n := range whole {
		if _, err := Into(t.TempDir(), bytes.NewReader(whole[:n])); !errors.Is(err, ErrArchive) {
			t.Fatalf("the first %d of its %d bytes: wanted %v, got %v", n, len(whole), ErrArchive, err)
		}
	}
	if _, err := Into(t.TempDir(), bytes.NewReader(whole)); err != nil {
		t.Errorf("the whole of it: %v", err)
	}
	// Nor may anything ride behind a whole one: more bytes, or a second archive.
	for what, more := range map[string][]byte{"a byte": {0}, "text": []byte("more"), "itself again": whole} {
		if _, err := Into(t.TempDir(), bytes.NewReader(append(bytes.Clone(whole), more...))); !errors.Is(err, ErrArchive) {
			t.Errorf("the whole of it with %s after it: wanted %v, got %v", what, ErrArchive, err)
		}
	}
}

// An archive that unpacks to more than it should is given up on, not followed to the end.
func TestAnArchiveIsHeldToWhatAReleaseHolds(t *testing.T) {
	three := archive(t, entry{name: "a", body: "12345"}, entry{name: "b", body: "12345"}, entry{name: "c", body: "12345"})
	for _, tt := range []struct {
		what     string
		files    int
		bytes    int64
		wantFail bool
	}{
		{"room for all of it", 3, 15, false},
		{"room for two files", 2, 15, true},
		{"room for fourteen bytes", 3, 14, true},
		{"room for one file of five bytes", 1, 5, true},
	} {
		dir := t.TempDir()
		_, err := into(dir, bytes.NewReader(three), tt.files, tt.bytes)
		if tt.wantFail != errors.Is(err, ErrArchive) || (!tt.wantFail && err != nil) {
			t.Errorf("%s: wanted it refused: %v, got %v", tt.what, tt.wantFail, err)
		}
		// Refusing afterwards is too late: what there was no room for must never reach the disk.
		written, _ := os.ReadDir(dir)
		var size int64
		for _, f := range written {
			info, err := f.Info()
			if err != nil {
				t.Fatal(err)
			}
			size += info.Size()
		}
		if len(written) > tt.files || size > tt.bytes {
			t.Errorf("%s: wanted at most %d files and %d bytes on the disk, got %d and %d", tt.what, tt.files, tt.bytes, len(written), size)
		}
	}
}
