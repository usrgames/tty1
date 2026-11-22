// Package unpack unpacks the archive of a release, and little else: a release's archive is a
// handful of plain files side by side, the program and its licence, and this refuses whatever is
// not that. tty1 only unpacks an archive whose hash a signed manifest vouches for, so this is the
// second line, not the first: it keeps a mistake in a build, or a build someone got into, from
// writing anywhere but the folder it was given.
package unpack

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ErrArchive is why an archive is refused. The details are added to it.
var ErrArchive = errors.New("the archive is not one tty1 will unpack")

// What an archive may hold at most. A release holds a few files of a few megabytes.
const (
	MaxFiles = 64
	MaxBytes = 1 << 30
)

// Into unpacks the gzipped tar in r into dir, a folder the caller made for it and nothing else is
// in. It writes plain files only, straight into dir, under plain names, and returns the names.
// Where it stops with an error it may have written some of them: the folder is the caller's to
// throw away.
func Into(dir string, r io.Reader) ([]string, error) { return into(dir, r, MaxFiles, MaxBytes) }

func into(dir string, r io.Reader, maxFiles int, maxBytes int64) ([]string, error) {
	refuse := func(format string, args ...any) ([]string, error) {
		return nil, fmt.Errorf("%w: %s", ErrArchive, fmt.Sprintf(format, args...))
	}
	gz, err := gzip.NewReader(r)
	if err != nil {
		return refuse("it is not gzipped: %v", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	var names []string
	folder := false
	left := maxBytes
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return refuse("it cannot be read to its end: %v", err)
		}
		// The top folder itself, which tar writes as "./", is no file and is passed over: once,
		// as tar writes it. Any number of them would be read at no cost to the budgets below.
		if h.Typeflag == tar.TypeDir && (h.Name == "./" || h.Name == ".") {
			if folder {
				return refuse("it names its own folder twice")
			}
			folder = true
			continue
		}
		name := strings.TrimPrefix(h.Name, "./")
		switch {
		case h.Typeflag != tar.TypeReg:
			return refuse("%q is not a plain file: a link, a folder or a device has no place in a release", h.Name)
		case !plain(name):
			return refuse("%q is not a plain file name", h.Name)
		case len(names) == maxFiles:
			return refuse("it holds more than %d files", maxFiles)
		case h.Size < 0 || h.Size > left:
			return refuse("it unpacks to more than %d bytes", maxBytes)
		}
		left -= h.Size

		// The program keeps its right to be run, and nothing gets more than that.
		mode := os.FileMode(0o644)
		if h.Mode&0o111 != 0 {
			mode = 0o755
		}
		// O_EXCL: a name used twice, or one that differs only in case on a disk that does not
		// tell the two apart, is refused here.
		f, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				return refuse("it holds %q twice", name)
			}
			return nil, err
		}
		_, err = io.CopyN(f, tr, h.Size)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return refuse("%q cannot be read to its end: %v", name, err)
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return refuse("it holds no file")
	}
	// tar stops at its own end mark, which is before the end of the gzip stream: read on to
	// that end, or an archive cut off in its last bytes, where gzip keeps its checksum, would
	// pass. What tar leaves after its end mark is padding, all zeros, and nothing else may be.
	pad := make([]byte, 32<<10)
	for {
		n, err := gz.Read(pad)
		if strings.Trim(string(pad[:n]), "\x00") != "" {
			return refuse("something follows its end")
		}
		if left -= int64(n); left < 0 {
			return refuse("it unpacks to more than %d bytes", maxBytes)
		}
		if err == io.EOF {
			return names, nil
		}
		if err != nil {
			return refuse("it cannot be read to its end: %v", err)
		}
	}
}

// plain reports whether a name is a file's name and nothing more: no folder, no way up, nothing
// hidden, nothing a terminal would choke on.
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
