package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"usr.games/tty1/internal/fetch"
	"usr.games/tty1/internal/release/releasetest"
	"usr.games/tty1/internal/shelf"
)

// world is everything tty1 touches, made for one test: a studio with its own keys, one local
// server that plays both the site and GitHub's releases, a shelf in a temporary folder, and a
// tty1 wired to them. The check of a release is the real one, against the test's studio.
type world struct {
	t      *testing.T
	studio *releasetest.Studio
	files  map[string][]byte // what the server answers, by path
	app    *App
	out    bytes.Buffer
	err    bytes.Buffer
}

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{t: t, studio: releasetest.New(t), files: map[string][]byte{}}
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		body, ok := w.files[r.URL.Path]
		if !ok {
			http.NotFound(rw, r)
			return
		}
		rw.Write(body)
	}))
	t.Cleanup(srv.Close)
	games, err := shelf.At(filepath.Join(t.TempDir(), "tty1"))
	if err != nil {
		t.Fatal(err)
	}
	self := filepath.Join(t.TempDir(), "tty1")
	if err := os.WriteFile(self, []byte("the tty1 that is running"), 0o755); err != nil {
		t.Fatal(err)
	}
	w.app = &App{
		Out: &w.out, Err: &w.err,
		Shelf:   games,
		Net:     fetch.Client{Site: srv.URL, Releases: srv.URL + "/usrgames"},
		Signers: w.studio.Signers,
		Now:     time.Now,
		OS:      "linux", Arch: "amd64",
		Version: "v1.0.0", Released: true, Self: self,
		// A test cannot give its own process away, so the game is run and its words kept.
		Exec: func(program string, args []string) error {
			cmd := exec.Command(program, args[1:]...)
			cmd.Stdout = &w.out
			return cmd.Run()
		},
	}
	w.list("breakshot", "rustwake")
	return w
}

// list makes the site list these games.
func (w *world) list(names ...string) {
	var games []string
	for _, n := range names {
		games = append(games, fmt.Sprintf(`{"name": %q, "players": "1p", "blurb": "A game called %s."}`, n, n))
	}
	w.files["/etc/games.json"] = []byte(`{"schema": 1, "games": [` + strings.Join(games, ",") + `]}`)
}

// run runs tty1 with a command line and returns its exit status and what it printed.
func (w *world) run(args ...string) (status int, out, errs string) {
	w.t.Helper()
	w.out.Reset()
	w.err.Reset()
	status = w.app.Run(context.Background(), args)
	return status, w.out.String(), w.err.String()
}

// entry is a file in an archive made for a test.
type entry struct {
	name, body string
	kind       byte
	link       string
}

func pack(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Typeflag: e.kind, Linkname: e.link, Mode: 0o755, Size: int64(len(e.body))}
		if e.kind == 0 {
			h.Typeflag = tar.TypeReg
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(e.body))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

// rel is a release to publish, and how it differs from a good one. The zero value of each field
// is the good release: the right name in the manifest, signed by the studio, with an archive for
// the test's machine holding the program, which says its name, its version and what it was told.
type rel struct {
	manifestName string              // "" means the name it is published under
	signer       *releasetest.Studio // nil means the world's studio
	archive      []byte              // nil means a good archive
	served       []byte              // nil means the archive the manifest names is the one served
	os           string              // "" means linux
	schema       int                 // 0 means 1
}

// publish makes name's newest release be version, as r says.
func (w *world) publish(name, version string, r rel) {
	w.t.Helper()
	if r.manifestName == "" {
		r.manifestName = name
	}
	if r.signer == nil {
		r.signer = w.studio
	}
	if r.os == "" {
		r.os = "linux"
	}
	if r.schema == 0 {
		r.schema = 1
	}
	if r.archive == nil {
		program := fmt.Sprintf("#!/bin/sh\necho \"%s %s $*\"\n", name, version)
		r.archive = pack(w.t, entry{name: "./", kind: tar.TypeDir}, entry{name: "./LICENSE", body: "MIT\n"}, entry{name: "./" + name, body: program})
	}
	if r.served == nil {
		r.served = r.archive
	}
	sum := sha256.Sum256(r.archive)
	file := name + "_" + r.os + "_amd64.tar.gz"
	manifest := []byte(fmt.Sprintf(`{"schema": %d, "name": %q, "version": %q, "files": [{"name": %q, "size": %d, "sha256": %q, "os": %q, "arch": "amd64"}]}`,
		r.schema, r.manifestName, version, file, len(r.archive), hex.EncodeToString(sum[:]), r.os))
	repo := name + ".6"
	if name == "tty1" {
		repo = name
	}
	base := "/usrgames/" + repo + "/releases/"
	w.files[base+"latest/download/manifest.json"] = manifest
	w.files[base+"latest/download/manifest.json.sig"] = r.signer.Sign(manifest)
	w.files[base+"download/"+version+"/"+file] = r.served
}

// clean reports what is on the shelf besides whole games: nothing may be left half done.
func (w *world) clean() {
	w.t.Helper()
	left, _ := os.ReadDir(filepath.Join(w.app.Shelf.Dir(), "staging"))
	for _, e := range left {
		w.t.Errorf("wanted nothing left in staging, found %s", e.Name())
	}
}

func TestAGameIsBroughtHomeStartedAndKeptCurrent(t *testing.T) {
	w := newWorld(t)
	w.publish("rustwake", "v0.1.0", rel{})

	if status, out, errs := w.run("ls"); status != 0 || !strings.Contains(out, "rustwake(6)") || !strings.Contains(out, "not installed, v0.1.0 is out") || !strings.Contains(out, "breakshot(6)") || !strings.Contains(out, "no release yet") {
		t.Fatalf("ls before anything is installed: status %d\n%s%s", status, out, errs)
	}
	if status, _, errs := w.run("run", "rustwake"); status != 1 || !strings.Contains(errs, "tty1 install rustwake") {
		t.Errorf("run before it is installed: wanted to be told how to install it, got %d: %s", status, errs)
	}
	if status, out, errs := w.run("install", "rustwake"); status != 0 || !strings.Contains(out, "rustwake v0.1.0 is home") {
		t.Fatalf("install: status %d\n%s%s", status, out, errs)
	}
	w.clean()
	if status, out, errs := w.run("run", "rustwake", "--fast", "two words"); status != 0 || out != "rustwake v0.1.0 --fast two words\n" {
		t.Errorf("run: wanted the game started with what followed its name, got %d: %q %s", status, out, errs)
	}
	if status, out, _ := w.run("install", "rustwake"); status != 0 || !strings.Contains(out, "already here") {
		t.Errorf("install again: wanted to be told it is already here, got %d: %s", status, out)
	}
	if status, out, _ := w.run("ls"); status != 0 || !strings.Contains(out, "v0.1.0") || strings.Contains(out, "is out") {
		t.Errorf("ls once it is current: got %d\n%s", status, out)
	}

	w.publish("rustwake", "v0.2.0", rel{})
	if status, out, _ := w.run("ls"); status != 0 || !strings.Contains(out, "v0.1.0, v0.2.0 is out") {
		t.Errorf("ls with a newer release out: got %d\n%s", status, out)
	}
	if status, out, errs := w.run("update"); status != 0 || !strings.Contains(out, "rustwake is now v0.2.0, up from v0.1.0") {
		t.Fatalf("update: status %d\n%s%s", status, out, errs)
	}
	w.clean()
	if _, out, _ := w.run("run", "rustwake"); out != "rustwake v0.2.0 \n" {
		t.Errorf("run after the update: wanted the new program, got %q", out)
	}
	// What was unpacked is exactly what the archive held, and the manifest that vouched for it.
	entries, _ := os.ReadDir(filepath.Join(w.app.Shelf.Dir(), "games", "rustwake"))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(names, " ") != "LICENSE manifest.json rustwake" {
		t.Errorf("wanted the licence, the manifest and the program in the game's folder, got %v", names)
	}
}

// Every way an install has to stop. Each time: tty1 says so and fails, the game that was there is
// as it was, and nothing is left half done.
func TestAnInstallStopsBeforeAnythingIsPutInPlace(t *testing.T) {
	good := func(w *world) []byte {
		return pack(w.t, entry{name: "rustwake", body: "#!/bin/sh\necho forged\n"})
	}
	for _, tt := range []struct {
		what    string
		publish func(w *world)
		says    string
	}{
		{"a release signed by someone else", func(w *world) { w.publish("rustwake", "v0.2.0", rel{signer: releasetest.New(w.t)}) }, "does not check out"},
		{"the studio's manifest of another program", func(w *world) { w.publish("rustwake", "v0.2.0", rel{manifestName: "breakshot"}) }, "does not check out"},
		{"a manifest of a kind not known", func(w *world) { w.publish("rustwake", "v0.2.0", rel{schema: 2}) }, "does not check out"},
		{"a version that is none", func(w *world) { w.publish("rustwake", "latest", rel{}) }, "not a version"},
		{"an older release where the newest should be", func(w *world) { w.publish("rustwake", "v0.0.9", rel{}) }, "does not go back"},
		{"a pre-release of what is installed", func(w *world) { w.publish("rustwake", "v0.1.0-rc.1", rel{}) }, "does not go back"},
		{"no build for this machine", func(w *world) { w.publish("rustwake", "v0.2.0", rel{os: "darwin"}) }, "no build for linux"},
		{"an archive other than the one the manifest names", func(w *world) { w.publish("rustwake", "v0.2.0", rel{served: good(w)}) }, "not what the release names"},
		{"an archive cut short on the way", func(w *world) {
			a := pack(w.t, entry{name: "rustwake", body: strings.Repeat("x", 5000)})
			w.publish("rustwake", "v0.2.0", rel{archive: a, served: a[:len(a)/2]})
		}, "not what the release names"},
		{"an archive with more after it", func(w *world) {
			a := pack(w.t, entry{name: "rustwake", body: "x"})
			w.publish("rustwake", "v0.2.0", rel{archive: a, served: append(bytes.Clone(a), "more"...)})
		}, "not what the release names"},
		{"a release whose archive is missing", func(w *world) {
			w.publish("rustwake", "v0.2.0", rel{})
			delete(w.files, "/usrgames/rustwake.6/releases/download/v0.2.0/rustwake_linux_amd64.tar.gz")
		}, "does not hold it"},
		{"a signed archive that reaches out of its folder", func(w *world) {
			w.publish("rustwake", "v0.2.0", rel{archive: pack(w.t, entry{name: "rustwake", body: "x"}, entry{name: "../escaped", body: "x"})})
		}, "not a plain file name"},
		{"a signed archive holding a link", func(w *world) {
			w.publish("rustwake", "v0.2.0", rel{archive: pack(w.t, entry{name: "rustwake", body: "x"}, entry{name: "way-out", kind: tar.TypeSymlink, link: "/"})})
		}, "not a plain file"},
		{"a signed archive without the program", func(w *world) {
			w.publish("rustwake", "v0.2.0", rel{archive: pack(w.t, entry{name: "LICENSE", body: "MIT\n"})})
		}, "holds no program named rustwake"},
	} {
		for _, command := range []string{"install", "update"} {
			w := newWorld(t)
			w.publish("rustwake", "v0.1.0", rel{})
			if status, _, errs := w.run("install", "rustwake"); status != 0 {
				t.Fatalf("%s: the first install: %s", tt.what, errs)
			}
			tt.publish(w)
			status, out, errs := w.run(command, "rustwake")
			if status != 1 || !strings.Contains(errs, tt.says) {
				t.Errorf("%s, on %s: wanted it to fail saying %q, got %d: %s%s", tt.what, command, tt.says, status, out, errs)
			}
			if v, _ := w.app.Shelf.Version("rustwake"); v != "v0.1.0" {
				t.Errorf("%s, on %s: wanted v0.1.0 still installed, got %q", tt.what, command, v)
			}
			if _, out, _ := w.run("run", "rustwake"); out != "rustwake v0.1.0 \n" {
				t.Errorf("%s, on %s: wanted the game that was there to run as before, got %q", tt.what, command, out)
			}
			w.clean()
			if _, err := os.Lstat(filepath.Join(w.app.Shelf.Dir(), "escaped")); err == nil {
				t.Errorf("%s, on %s: a file was written outside the game's folder", tt.what, command)
			}
		}
	}
}

func TestWhatCannotBeAGameIsNotAskedFor(t *testing.T) {
	w := newWorld(t)
	asked := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { asked++ }))
	defer srv.Close()
	w.app.Net.Releases = srv.URL
	for _, name := range []string{"tty1", "arcade", "relay", "../etc", "Rustwake", "rust wake", "rustwake/../../x", ""} {
		for _, command := range []string{"install", "update", "run"} {
			if status, _, errs := w.run(command, name); status != 1 || errs == "" {
				t.Errorf("%s %q: wanted it refused with a reason, got %d: %s", command, name, status, errs)
			}
		}
	}
	if asked != 0 {
		t.Errorf("wanted nothing asked of the network for names that cannot be games, got %d requests", asked)
	}

	// A name is never a path. A program lying beside the games, where a name with a way up in
	// it would lead, is not started.
	outside := filepath.Join(w.app.Shelf.Dir(), "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(outside, "outside"), []byte("#!/bin/sh\necho started\n"), 0o755)
	os.WriteFile(filepath.Join(outside, "manifest.json"), []byte(`{"schema": 1, "name": "outside", "version": "v1.0.0"}`), 0o644)
	if status, out, errs := w.run("run", "../outside"); status != 1 || out != "" || !strings.Contains(errs, "not the name of a game") {
		t.Errorf("run ../outside: wanted it refused as no name of a game and nothing started, got %d: %q %s", status, out, errs)
	}
}

// Keeping a game current is not bringing it home: a game that is not installed is left alone.
func TestUpdateDoesNotInstall(t *testing.T) {
	w := newWorld(t)
	w.publish("rustwake", "v0.1.0", rel{})
	if status, _, errs := w.run("update", "rustwake"); status != 1 || !strings.Contains(errs, "tty1 install rustwake") {
		t.Errorf("update of a game not installed: wanted to be told how to install it, got %d: %s", status, errs)
	}
	if _, installed := w.app.Shelf.Version("rustwake"); installed {
		t.Error("wanted nothing installed by an update")
	}
	if status, out, errs := w.run("update"); status != 0 || strings.Contains(out+errs, "rustwake") {
		t.Errorf("update with nothing installed: wanted no word of rustwake, got %d: %s%s", status, out, errs)
	}
}

func TestWithNoNetworkWhatIsInstalledStillShowsAndRuns(t *testing.T) {
	w := newWorld(t)
	w.publish("rustwake", "v0.1.0", rel{})
	if status, _, errs := w.run("install", "rustwake"); status != 0 {
		t.Fatal(errs)
	}
	dead := httptest.NewServer(http.NotFoundHandler())
	w.app.Net.Site, w.app.Net.Releases = dead.URL, dead.URL
	dead.Close()

	if status, out, errs := w.run("ls"); status != 0 || !strings.Contains(out, "rustwake(6)") || !strings.Contains(out, "v0.1.0, newest unknown") || !strings.Contains(errs, "could not be reached") {
		t.Errorf("ls: wanted what is installed, and to be told the list could not be read, got %d\n%s%s", status, out, errs)
	}
	if status, out, _ := w.run("run", "rustwake"); status != 0 || out != "rustwake v0.1.0 \n" {
		t.Errorf("run: wanted the game started with no network, got %d: %q", status, out)
	}
	for _, command := range [][]string{{"install", "breakshot"}, {"update", "rustwake"}, {"update"}} {
		if status, _, errs := w.run(command...); status != 1 || !strings.Contains(errs, "could not be reached") {
			t.Errorf("%v: wanted it to fail saying the network could not be reached, got %d: %s", command, status, errs)
		}
	}
	// With nothing installed there is nothing to show.
	empty := newWorld(t)
	empty.app.Net.Site = dead.URL
	if status, _, _ := empty.run("ls"); status != 1 {
		t.Errorf("ls with no network and nothing installed: wanted it to fail, got %d", status)
	}
}

func TestAGameInstalledAndNoLongerListedIsStillShown(t *testing.T) {
	w := newWorld(t)
	w.publish("rustwake", "v0.1.0", rel{})
	w.run("install", "rustwake")
	w.list("breakshot")
	if status, out, _ := w.run("ls"); status != 0 || !strings.Contains(out, "rustwake(6)") || !strings.Contains(out, "no longer on the list") {
		t.Errorf("wanted rustwake shown as installed though the list dropped it, got %d\n%s", status, out)
	}
}

// The list is the site's word and no more. Whatever it says is printed as words: nothing in it
// may move the cursor or change a colour in the player's terminal.
func TestTheListIsPrintedAsWordsOnly(t *testing.T) {
	w := newWorld(t)
	w.files["/etc/games.json"] = []byte(`{"schema": 1, "games": [{"name": "rustwake", "players": "1p\u001b[2J\u009b2J", "blurb": "Clear\u001b[31m the\u0007 plant.\r\nsecond\u2028 line\u202e"}]}`)
	status, out, _ := w.run("ls")
	if status != 0 || len(acted(out)) != 0 || strings.Count(out, "\n") != 1 || !strings.Contains(out, "Clear[31m the plant.second line") {
		t.Errorf("wanted one line of words, got %d: %q", status, out)
	}
}

func TestTty1ReplacesItselfWholeOrNotAtAll(t *testing.T) {
	read := func(w *world) string {
		b, err := os.ReadFile(w.app.Self)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	newer := func(w *world, version string, r rel) {
		if r.archive == nil {
			r.archive = pack(t, entry{name: "./LICENSE", body: "MIT\n"}, entry{name: "./tty1", body: "tty1 " + version})
		}
		w.publish("tty1", version, r)
	}

	w := newWorld(t)
	if status, out, errs := w.run("update"); status != 0 || read(w) != "the tty1 that is running" {
		t.Errorf("with no release of tty1: wanted it left alone, got %d: %s%s", status, out, errs)
	}
	newer(w, "v1.0.0", rel{})
	if status, out, _ := w.run("update"); status != 0 || !strings.Contains(out, "tty1 v1.0.0 is current") || read(w) != "the tty1 that is running" {
		t.Errorf("with the same release out: wanted it left alone and said to be current, got %d: %s", status, out)
	}
	newer(w, "v1.1.0", rel{})
	if status, out, errs := w.run("update"); status != 0 || !strings.Contains(out, "tty1 is now v1.1.0, up from v1.0.0") || read(w) != "tty1 v1.1.0" {
		t.Fatalf("with a newer release out: wanted it replaced, got %d: %s%s and %q", status, out, errs, read(w))
	}
	if info, _ := os.Stat(w.app.Self); info.Mode().Perm() != 0o755 {
		t.Errorf("wanted the new tty1 to be a program, got mode %o", info.Mode().Perm())
	}
	if left, _ := os.ReadDir(filepath.Dir(w.app.Self)); len(left) != 1 {
		t.Errorf("wanted nothing left beside the new tty1, got %d entries in its folder", len(left))
	}
	w.clean()

	for _, tt := range []struct {
		what    string
		arrange func(w *world)
		status  int
		says    string
	}{
		{"an older release where the newest should be", func(w *world) { newer(w, "v0.9.0", rel{}) }, 1, "does not go back"},
		{"a release signed by someone else", func(w *world) { newer(w, "v2.0.0", rel{signer: releasetest.New(t)}) }, 1, "does not check out"},
		{"an archive other than the one named", func(w *world) { newer(w, "v2.0.0", rel{served: pack(t, entry{name: "tty1", body: "forged"})}) }, 1, "not what the release names"},
		{"an archive with no tty1 in it", func(w *world) { newer(w, "v2.0.0", rel{archive: pack(t, entry{name: "LICENSE", body: "MIT\n"})}) }, 1, "holds no tty1"},
		{"a tty1 that no release built", func(w *world) { newer(w, "v2.0.0", rel{}); w.app.Released = false }, 0, "was not installed from a release"},
		{"a tty1 that cannot tell its own version", func(w *world) { newer(w, "v2.0.0", rel{}); w.app.Version = "dev" }, 1, "cannot tell its own version"},
		{"a folder tty1 may not write in", func(w *world) {
			newer(w, "v2.0.0", rel{})
			os.Chmod(filepath.Dir(w.app.Self), 0o555)
			t.Cleanup(func() { os.Chmod(filepath.Dir(w.app.Self), 0o755) })
		}, 1, "left itself as it is"},
	} {
		if tt.what == "a folder tty1 may not write in" && os.Geteuid() == 0 {
			continue // root may write anywhere, so there is no such folder to try
		}
		w := newWorld(t)
		tt.arrange(w)
		status, out, errs := w.run("update")
		if status != tt.status || !strings.Contains(out+errs, tt.says) {
			t.Errorf("%s: wanted status %d saying %q, got %d: %s%s", tt.what, tt.status, tt.says, status, out, errs)
		}
		if got := read(w); got != "the tty1 that is running" {
			t.Errorf("%s: wanted the running tty1 untouched, got %q", tt.what, got)
		}
		w.clean()
	}
}

func TestTheCommandLineIsUnderstoodOrSaidNotToBe(t *testing.T) {
	w := newWorld(t)
	for _, args := range [][]string{nil, {"help"}, {"-h"}, {"--help"}} {
		if status, out, _ := w.run(args...); status != 0 || !strings.Contains(out, "tty1 install NAME") {
			t.Errorf("%v: wanted the help, got %d: %s", args, status, out)
		}
	}
	for _, args := range [][]string{{"--version"}, {"version"}} {
		if status, out, _ := w.run(args...); status != 0 || out != "tty1 v1.0.0\n" {
			t.Errorf("%v: wanted the version, got %d: %q", args, status, out)
		}
	}
	for _, args := range [][]string{{"ls", "more"}, {"install"}, {"install", "a", "b"}, {"update", "a", "b"}, {"run"}, {"rustwake"}, {"--frobnicate"}} {
		if status, out, errs := w.run(args...); status != 2 || out != "" || !strings.Contains(errs, "tty1 help") {
			t.Errorf("%v: wanted status 2 and a pointer to the help, got %d: %s%s", args, status, out, errs)
		}
	}
	if _, _, errs := w.run("rustwake"); !strings.Contains(errs, "tty1 run rustwake") {
		t.Errorf("a game's name alone: wanted to be told how to start it, got %s", errs)
	}
}
