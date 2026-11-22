package fetch

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// serve starts a local server that answers each path with what the map gives it: a body, or a
// status when the body is one of "404", "500". It plays the site and the releases at once.
func serve(t *testing.T, answers map[string]string) (Client, *[]string) {
	t.Helper()
	var agents []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agents = append(agents, r.Header.Get("User-Agent"))
		switch body, ok := answers[r.URL.Path]; {
		case !ok || body == "404":
			http.NotFound(w, r)
		case body == "500":
			http.Error(w, "broken", http.StatusInternalServerError)
		default:
			io.WriteString(w, body)
		}
	}))
	t.Cleanup(srv.Close)
	return Client{Site: srv.URL, Releases: srv.URL + "/usrgames", Agent: "tty1/test"}, &agents
}

const list = `{"schema": 1, "games": [{"name": "breakshot", "players": "2p net", "blurb": "Eight-ball pool."}, {"name": "rustwake", "players": "1p", "blurb": "Rogue machines."}]}`

func TestTheSitesListIsRead(t *testing.T) {
	c, agents := serve(t, map[string]string{"/etc/games.json": list})
	games, err := c.Games(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(games) != 2 || games[0] != (Game{"breakshot", "2p net", "Eight-ball pool."}) || games[1].Name != "rustwake" {
		t.Errorf("wanted breakshot and rustwake as the site lists them, got %+v", games)
	}
	if len(*agents) != 1 || (*agents)[0] != "tty1/test" {
		t.Errorf("wanted tty1 to say who is asking, got %q", *agents)
	}
}

func TestAListTty1CannotUseIsRefusedWhole(t *testing.T) {
	for what, body := range map[string]string{
		"no list where one should be":   "404",
		"a site that is broken":         "500",
		"text that is not JSON":         "<html>",
		"a list of a kind not known":    `{"schema": 2, "games": []}`,
		"a list of no kind":             `{"games": []}`,
		"a game with a path for a name": `{"schema": 1, "games": [{"name": "../etc"}]}`,
		"a game with a capital":         `{"schema": 1, "games": [{"name": "Rustwake"}]}`,
		"a game with no name":           `{"schema": 1, "games": [{"players": "1p"}]}`,
		"a list far too long":           `{"schema": 1, "games": [], "padding": "` + strings.Repeat("x", maxList) + `"}`,
	} {
		c, _ := serve(t, map[string]string{"/etc/games.json": body})
		games, err := c.Games(context.Background())
		if !errors.Is(err, ErrAnswer) || games != nil {
			t.Errorf("%s: wanted %v and no games, got %v and %v", what, ErrAnswer, games, err)
		}
		// Too long is refused for being too long, not for whatever its first megabyte happens to say.
		if what == "a list far too long" && !strings.Contains(err.Error(), "more than") {
			t.Errorf("%s: wanted it refused for its size, got %v", what, err)
		}
	}
}

func TestTheNewestReleaseIsAskedOfItsRepository(t *testing.T) {
	c, _ := serve(t, map[string]string{
		"/usrgames/rustwake/releases/latest/download/manifest.json":     `{"schema": 1}`,
		"/usrgames/rustwake/releases/latest/download/manifest.json.sig": "a signature",
		"/usrgames/nosig/releases/latest/download/manifest.json":        `{"schema": 1}`,
		"/usrgames/broken/releases/latest/download/manifest.json":       "500",
		"/usrgames/huge/releases/latest/download/manifest.json":         strings.Repeat("x", maxManifest+1),
		"/usrgames/rustwake/releases/download/v1.0.0/rustwake.tar.gz":   "the archive",
	})
	manifest, sig, err := c.Newest(context.Background(), "rustwake")
	if err != nil || string(manifest) != `{"schema": 1}` || string(sig) != "a signature" {
		t.Errorf("wanted the manifest and its signature as served, got %q, %q and %v", manifest, sig, err)
	}
	for name, want := range map[string]error{"skyspear": ErrNoRelease, "nosig": ErrAnswer, "broken": ErrAnswer, "huge": ErrAnswer} {
		m, s, err := c.Newest(context.Background(), name)
		if !errors.Is(err, want) || m != nil || s != nil {
			t.Errorf("%s: wanted %v and nothing else, got %v, %q, %q", name, want, err, m, s)
		}
		if name == "huge" && !strings.Contains(err.Error(), "more than") {
			t.Errorf("%s: wanted it refused for its size, got %v", name, err)
		}
	}

	body, err := c.File(context.Background(), "rustwake", "v1.0.0", "rustwake.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(body)
	body.Close()
	if string(got) != "the archive" {
		t.Errorf("wanted the archive as served, got %q", got)
	}
	if _, err := c.File(context.Background(), "rustwake", "v1.0.0", "missing.tar.gz"); !errors.Is(err, ErrAnswer) {
		t.Errorf("a file the release does not hold: wanted %v, got %v", ErrAnswer, err)
	}
}

func TestNoNetworkIsSaidAsNoNetwork(t *testing.T) {
	c, _ := serve(t, nil)
	srv := httptest.NewServer(http.NotFoundHandler())
	c.Site, c.Releases = srv.URL, srv.URL
	srv.Close()
	if _, err := c.Games(context.Background()); !errors.Is(err, ErrUnreachable) {
		t.Errorf("the list: wanted %v, got %v", ErrUnreachable, err)
	}
	if _, _, err := c.Newest(context.Background(), "rustwake"); !errors.Is(err, ErrUnreachable) {
		t.Errorf("a release: wanted %v, got %v", ErrUnreachable, err)
	}
	if _, err := c.File(context.Background(), "rustwake", "v1.0.0", "x"); !errors.Is(err, ErrUnreachable) {
		t.Errorf("a file: wanted %v, got %v", ErrUnreachable, err)
	}
}

// GitHub hands a download on to another of its hosts, so tty1 follows redirects. It does not
// follow one out of https, and it does not follow them for ever.
func TestARequestMadeInPrivateIsNotFinishedInTheOpen(t *testing.T) {
	req := func(address string) *http.Request {
		u, err := url.Parse(address)
		if err != nil {
			t.Fatal(err)
		}
		return &http.Request{URL: u}
	}
	for _, tt := range []struct {
		what, from, to string
		ok             bool
	}{
		{"https to https on another host", "https://github.com/a", "https://objects.githubusercontent.com/b", true},
		{"http to http, as a test's own server does", "http://127.0.0.1:1/a", "http://127.0.0.1:1/b", true},
		{"https to http", "https://github.com/a", "http://github.com/a", false},
		{"https to a file", "https://github.com/a", "file:///etc/passwd", false},
	} {
		if err := redirect(req(tt.to), []*http.Request{req(tt.from)}); (err == nil) != tt.ok {
			t.Errorf("%s: wanted it followed: %v, got %v", tt.what, tt.ok, err)
		}
	}
	via := make([]*http.Request, 10)
	for i := range via {
		via[i] = req("https://github.com/a")
	}
	if redirect(req("https://github.com/b"), via) == nil {
		t.Error("wanted the eleventh redirect in a row refused")
	}
}

func TestOnlyAPlainNameGoesIntoAnAddress(t *testing.T) {
	for _, name := range []string{"rustwake", "skyspear", "tty1", "a", "game2"} {
		if !Name(name) {
			t.Errorf("%q: wanted it taken for a name", name)
		}
	}
	for _, name := range []string{"", "Rustwake", "1up", "rust-wake", "rust_wake", "rust wake", "rust/wake", "../rustwake", ".", "rustwake.git", "rustwake?x=1", "rüstwake", strings.Repeat("a", 41)} {
		if Name(name) {
			t.Errorf("%q: wanted it refused as a name", name)
		}
	}
}
