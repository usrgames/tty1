package cli

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode"
)

// acted returns the characters in s that a terminal would act on and not show: every control
// character but the line's own end, and the marks that turn text around.
func acted(s string) []string {
	var found []string
	for _, r := range s {
		if (unicode.IsControl(r) && r != '\n') || unicode.Is(unicode.Bidi_Control, r) || r == ' ' || r == ' ' {
			found = append(found, fmt.Sprintf("U+%04X", r))
		}
	}
	return found
}

func TestCleanLeavesWordsAndNothingATerminalActsOn(t *testing.T) {
	for in, want := range map[string]string{
		"Clear the plant.":            "Clear the plant.",
		"café, 日本語, ½":                "café, 日本語, ½",
		"a\x1b[2Jb":                   "a[2Jb",
		"bell\a, tab\t, line\r\nnext": "bell, tab, linenext",
		"del\x7f":                     "del",
		"csi\u009b31m":                "csi31m",          // one character that starts a command, above 0x7f
		"title\u009d0;owned\u009c":    "title0;owned",    // a title for the window
		"backwards‮, ⁦":               "backwards, ",     // text turned around
		"line , paragraph ":           "line, paragraph", // a line's end by another name
		"a byte alone \x9b":           "a byte alone �",  // not a character at all: shown as one that is
	} {
		if got := Clean(in); got != want || len(acted(got)) != 0 {
			t.Errorf("Clean(%q): wanted %q, got %q, with %v left in it", in, want, got, acted(got))
		}
	}
}

// What the network says can end up in an error: the names in a certificate nobody trusts, the
// address of a redirect that is refused. An error goes to the terminal as words, as the list does.
func TestWhatTheNetworkSaysInAnErrorIsPrintedAsWordsOnly(t *testing.T) {
	const evil = "\x1b[2J\x1b[31mPWNED\x1b[0m"

	// A certificate signed by nobody, named for the attacker's text and not for the host.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "nobody"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		DNSNames: []string{evil}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	secure := httptest.NewUnstartedServer(http.NotFoundHandler())
	secure.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	secure.Config.ErrorLog = log.New(io.Discard, "", 0) // each refused handshake would be logged
	secure.StartTLS()
	defer secure.Close()
	_, port, _ := net.SplitHostPort(secure.Listener.Addr().String())
	// Go's own check of a certificate, which names what the certificate is valid for. With no
	// roots at all it is the same on every machine.
	goChecks := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: x509.NewCertPool()}}}

	// A server that sends tty1 round and round, and the tenth time to an address with a
	// character in it that starts a command.
	var hops atomic.Int32
	round := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n := hops.Add(1); n < 10 {
			http.Redirect(w, r, fmt.Sprintf("/again/%d", n), http.StatusFound)
			return
		}
		w.Header().Set("Location", "http://%c2%9b31mPWNED.example/x")
		w.WriteHeader(http.StatusFound)
	}))
	defer round.Close()

	for _, tt := range []struct {
		what, address, carries string
		client                 *http.Client
	}{
		{"a certificate named with escape sequences", "https://localhost:" + port, "PWNED", goChecks},
		{"a refused redirect to an address with a control character", round.URL, "PWNED.example", nil},
	} {
		for _, command := range [][]string{{"ls"}, {"install", "rustwake"}, {"update"}} {
			w := newWorld(t)
			w.app.Net.Site, w.app.Net.Releases, w.app.Net.HTTP = tt.address, tt.address, tt.client
			hops.Store(0)
			status, out, errs := w.run(command...)
			if status != 1 || !strings.Contains(errs, "could not be reached") {
				t.Errorf("%s, on %v: wanted status 1 and to be told the network could not be reached, got %d: %q", tt.what, command, status, errs)
			}
			// The attacker's words did arrive, so this is a test of how they are printed.
			if !strings.Contains(errs, tt.carries) {
				t.Errorf("%s, on %v: wanted the error to carry what the network said, or this proves nothing: %q", tt.what, command, errs)
			}
			if found := acted(out + errs); len(found) != 0 {
				t.Errorf("%s, on %v: wanted words only on the terminal, and %v went to it: %q", tt.what, command, found, errs)
			}
		}
	}
}

// The list decides how many games there are, and it is not trusted. ls asks about a few at a
// time, however many it lists.
func TestLsAsksAboutAFewGamesAtATime(t *testing.T) {
	w := newWorld(t)
	var names []string
	for i := range 60 {
		names = append(names, fmt.Sprintf("game%d", i))
	}
	w.list(names...)

	var mu sync.Mutex
	open, most, asked := 0, 0, 0
	held := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		mu.Lock()
		open++
		asked++
		most = max(most, open)
		mu.Unlock()
		time.Sleep(20 * time.Millisecond) // long enough for the others to pile up, if they could
		mu.Lock()
		open--
		mu.Unlock()
		http.NotFound(rw, r)
	}))
	defer held.Close()
	w.app.Net.Releases = held.URL

	status, out, errs := w.run("ls")
	if status != 0 || strings.Count(out, "\n") != 60 {
		t.Fatalf("wanted all 60 games listed, got status %d and %d lines: %s", status, strings.Count(out, "\n"), errs)
	}
	if asked != 60 {
		t.Errorf("wanted each game asked about once, got %d questions", asked)
	}
	if most > atOnce || most < 2 {
		t.Errorf("wanted between 2 and %d questions open at once, got %d", atOnce, most)
	}
}

// A folder on the shelf whose name is no game's name was not put there by tty1. It is not
// printed, whatever its name holds, and nothing is asked about it.
func TestAFolderOnTheShelfThatIsNoGameIsLeftOut(t *testing.T) {
	w := newWorld(t)
	w.publish("rustwake", "v0.1.0", rel{})
	if status, _, errs := w.run("install", "rustwake"); status != 0 {
		t.Fatal(errs)
	}
	for _, name := range []string{"\x1b[2Jx", "Loud", "two words"} {
		dir := filepath.Join(w.app.Shelf.Dir(), "games", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o755)
		os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"schema": 1, "name": "x", "version": "v1.0.0"}`), 0o644)
	}
	// ls asks about several games at once, so what it asked is written down under a lock.
	var mu sync.Mutex
	var paths []string
	seen := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		http.NotFound(rw, r)
	}))
	defer seen.Close()
	w.app.Net.Releases = seen.URL

	for _, command := range []string{"ls", "update"} {
		mu.Lock()
		paths = nil
		mu.Unlock()
		_, out, errs := w.run(command)
		mu.Lock()
		asked := slices.Clone(paths)
		mu.Unlock()
		if found := acted(out + errs); len(found) != 0 || strings.Contains(out+errs, "Loud") || strings.Contains(out+errs, "two words") {
			t.Errorf("%s: wanted the three folders left out, got %v in: %q %q", command, found, out, errs)
		}
		for _, p := range asked {
			if !strings.HasPrefix(p, "/rustwake.6/") && !strings.HasPrefix(p, "/breakshot.6/") && !strings.HasPrefix(p, "/tty1/") {
				t.Errorf("%s: wanted nothing asked about a folder that is no game, and %s was", command, p)
			}
		}
	}
	if _, out, _ := w.run("ls"); !strings.Contains(out, "rustwake(6)") {
		t.Errorf("wanted the installed game still listed, got:\n%s", out)
	}
}

// The new tty1 is written to a file of its own making and moved over the old one. What lies
// beside tty1 already, under any name, is neither written through nor given its mode to tty1.
func TestTty1ReplacesItselfThroughNothingThatLiesBesideIt(t *testing.T) {
	for what, leave := range map[string]func(t *testing.T, self, victim string){
		"a link under the name an update once used": func(t *testing.T, self, victim string) {
			if err := os.Symlink(victim, self+".new"); err != nil {
				t.Fatal(err)
			}
		},
		"a file anyone may write, under that name": func(t *testing.T, self, victim string) {
			if err := os.WriteFile(self+".new", []byte("left here"), 0o666); err != nil {
				t.Fatal(err)
			}
			os.Chmod(self+".new", 0o666)
		},
	} {
		w := newWorld(t)
		victim := filepath.Join(t.TempDir(), "victim")
		if err := os.WriteFile(victim, []byte("mine"), 0o644); err != nil {
			t.Fatal(err)
		}
		leave(t, w.app.Self, victim)
		w.publish("tty1", "v1.1.0", rel{archive: pack(t, entry{name: "./tty1", body: "tty1 v1.1.0"})})
		if status, _, errs := w.run("update"); status != 0 {
			t.Fatalf("%s: wanted the update done, got %d: %s", what, status, errs)
		}
		if got, _ := os.ReadFile(victim); string(got) != "mine" {
			t.Errorf("%s: wanted the file beside tty1's folder left alone, got %q", what, got)
		}
		info, err := os.Lstat(w.app.Self)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o755 {
			t.Errorf("%s: wanted tty1 a plain program of mode 755, got %v, %v", what, info, err)
		}
		if got, _ := os.ReadFile(w.app.Self); string(got) != "tty1 v1.1.0" {
			t.Errorf("%s: wanted the new tty1 in place, got %q", what, got)
		}
		left, _ := os.ReadDir(filepath.Dir(w.app.Self))
		if len(left) != 2 { // tty1, and what was left beside it, untouched
			t.Errorf("%s: wanted tty1 and what lay beside it and nothing more, got %d entries", what, len(left))
		}
	}
}
