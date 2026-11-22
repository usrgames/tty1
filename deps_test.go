package main

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// tty1 has one dependency, glonk, for the one question doctor asks a terminal. The packages
// that decide what is installed and run must never come to lean on it, or on anything else from
// outside: what tty1 trusts is settled by this repository and Go's standard library alone. The
// README says so, and this holds it.
func TestWhatDecidesAnInstallImportsTheStandardLibraryAlone(t *testing.T) {
	guarded := []string{"release", "unpack", "version", "fetch", "shelf"}
	for _, pkg := range guarded {
		files, err := filepath.Glob(filepath.Join("internal", pkg, "*.go"))
		if err != nil || len(files) == 0 {
			t.Fatalf("internal/%s: found no files to read: %v", pkg, err)
		}
		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, imp := range f.Imports {
				path, _ := strconv.Unquote(imp.Path.Value)
				if rest, ours := strings.CutPrefix(path, "usr.games/tty1/internal/"); ours && slices.Contains(guarded, rest) {
					continue
				}
				// A package of the standard library has no dot in the first part of its path.
				if first, _, _ := strings.Cut(path, "/"); strings.Contains(first, ".") {
					t.Errorf("%s imports %s: this package may import the standard library and its guarded neighbours only", file, path)
				}
			}
		}
	}
}
