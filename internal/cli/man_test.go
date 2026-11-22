package cli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// standInMan puts a man of the test's own first on the PATH, and nothing else. It says what it
// was handed and the page's first line, with the shell's own words only, since there is no other
// program for it to call.
func standInMan(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nread first < \"$1\"\necho \"man was handed $1\"\necho \"it begins: $first\"\n"
	if err := os.WriteFile(filepath.Join(dir, "man"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

const ownPage = ".TH TTY1 1\nthe page of tty1\n"

func TestManShowsTty1sOwnPage(t *testing.T) {
	w := newWorld(t)
	w.app.Manual = []byte(ownPage)
	standInMan(t)
	page := filepath.Join(w.app.Shelf.Dir(), "man", "tty1.1")
	for _, args := range [][]string{{"man"}, {"man", "tty1"}} {
		status, out, errs := w.run(args...)
		if status != 0 || out != "man was handed "+page+"\nit begins: .TH TTY1 1\n" {
			t.Errorf("%v: wanted man handed tty1's page at %s, got %d: %q %s", args, page, status, out, errs)
		}
	}
	if got, _ := os.ReadFile(page); string(got) != ownPage {
		t.Errorf("wanted the page as it is built in, got %q", got)
	}
	if info, _ := os.Stat(page); info.Mode().Perm() != 0o644 {
		t.Errorf("wanted a page anyone may read and only its owner write, got mode %o", info.Mode().Perm())
	}
	if left, _ := os.ReadDir(filepath.Dir(page)); len(left) != 1 {
		t.Errorf("wanted the page and nothing else in its folder, got %d entries", len(left))
	}
}

// The page is put in place by a move. Whatever is at its path, a link to another file included,
// is replaced and never written through.
func TestThePageIsNeverWrittenThroughALink(t *testing.T) {
	w := newWorld(t)
	w.app.Manual = []byte(ownPage)
	standInMan(t)
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(w.app.Shelf.Dir(), "man")
	os.MkdirAll(dir, 0o755)
	if err := os.Symlink(victim, filepath.Join(dir, "tty1.1")); err != nil {
		t.Fatal(err)
	}
	if status, _, errs := w.run("man"); status != 0 {
		t.Fatalf("wanted the page shown, got %d: %s", status, errs)
	}
	if got, _ := os.ReadFile(victim); string(got) != "mine" {
		t.Errorf("wanted the file the link led to left alone, got %q", got)
	}
	if info, err := os.Lstat(filepath.Join(dir, "tty1.1")); err != nil || !info.Mode().IsRegular() {
		t.Errorf("wanted a plain file where the link was, got %v, %v", info, err)
	}
}

func TestManShowsAnInstalledGamesPage(t *testing.T) {
	w := newWorld(t)
	w.publish("rustwake", "v0.1.0", rel{archive: pack(t,
		entry{name: "./rustwake", body: "#!/bin/sh\n"}, entry{name: "./rustwake.6", body: ".TH RUSTWAKE 6\n"}, entry{name: "./LICENSE", body: "MIT\n"})})
	w.publish("skyspear", "v0.1.0", rel{}) // a release from before its repository named a page
	for _, game := range []string{"rustwake", "skyspear"} {
		if status, _, errs := w.run("install", game); status != 0 {
			t.Fatal(errs)
		}
	}
	// A page is read from the game's own folder: nothing is asked of the network for it.
	asked := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { asked++ }))
	defer srv.Close()
	w.app.Net.Site, w.app.Net.Releases = srv.URL, srv.URL
	standInMan(t)

	page := filepath.Join(w.app.Shelf.Dir(), "games", "rustwake", "rustwake.6")
	if status, out, errs := w.run("man", "rustwake"); status != 0 || out != "man was handed "+page+"\nit begins: .TH RUSTWAKE 6\n" {
		t.Errorf("wanted man handed rustwake's page at %s, got %d: %q %s", page, status, out, errs)
	}
	for _, tt := range []struct{ name, says string }{
		{"skyspear", "skyspear came without a manual page"},
		{"breakshot", "tty1 install breakshot"},
		{"arcade", "is not a game"},
		{"../rustwake", "not the name of a game"},
		{"Rustwake", "not the name of a game"},
		{"", "not the name of a game"},
	} {
		if status, out, errs := w.run("man", tt.name); status != 1 || out != "" || !strings.Contains(errs, tt.says) {
			t.Errorf("man %q: wanted status 1 saying %q and nothing shown, got %d: %q %s", tt.name, tt.says, status, out, errs)
		}
	}
	if status, _, errs := w.run("man", "rustwake", "skyspear"); status != 2 || !strings.Contains(errs, "tty1 man takes one game") {
		t.Errorf("man with two names: wanted status 2, got %d: %s", status, errs)
	}
	if asked != 0 {
		t.Errorf("wanted nothing asked of the network, got %d requests", asked)
	}
	if _, out, _ := w.run("help"); !strings.Contains(out, "tty1 man [NAME]") {
		t.Errorf("wanted the help to list man, got:\n%s", out)
	}
}

func TestWithNoManOnTheMachineItSaysWhereThePageIs(t *testing.T) {
	w := newWorld(t)
	w.app.Manual = []byte(ownPage)
	t.Setenv("PATH", t.TempDir())
	page := filepath.Join(w.app.Shelf.Dir(), "man", "tty1.1")
	status, out, errs := w.run("man")
	if status != 1 || out != "" || !strings.Contains(errs, "has no man") || !strings.Contains(errs, page) {
		t.Errorf("wanted status 1, and to be told where the page is, got %d: %q %s", status, out, errs)
	}
	if got, _ := os.ReadFile(page); string(got) != ownPage {
		t.Errorf("wanted the page there to be read by other means, got %q", got)
	}
}
