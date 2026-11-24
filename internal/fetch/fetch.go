// Package fetch is what tty1 asks of the network, and it is two things: the site's list of games,
// and a program's releases, which GitHub holds. Nothing it brings back is trusted for having been
// fetched: the list only says what to offer, and a release is checked against the studio's
// signature by whoever asked for it.
package fetch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Where tty1 asks, unless it is told otherwise.
const (
	Site     = "https://usr.games"
	Releases = "https://github.com/usrgames"
)

// Why a request came to nothing.
var (
	ErrUnreachable = errors.New("could not be reached")
	ErrNoRelease   = errors.New("has no release yet")
	ErrAnswer      = errors.New("gave an answer tty1 cannot use")
)

// MaxGames is how many games a list may hold.
const MaxGames = 256

// What a small file may weigh at most. A list or a manifest is a few thousand bytes.
const (
	maxList      = 1 << 20
	maxManifest  = 1 << 20
	maxSignature = 64 << 10
)

// Client asks the site and the releases.
type Client struct {
	Site     string       // where etc/games.json is, such as https://usr.games
	Releases string       // where the release repositories are: Releases/NAME.6 for a game, Releases/tty1 for tty1
	Agent    string       // how tty1 names itself, such as tty1/v0.1.0
	HTTP     *http.Client // nil means one that follows a redirect only to https
}

// Game is one line of the site's list.
type Game struct {
	Name    string `json:"name"`
	Players string `json:"players"`
	Blurb   string `json:"blurb"`
}

// get asks for one address and returns the answer's body, which is the caller's to close.
func (c Client) get(ctx context.Context, address string) (io.ReadCloser, error) {
	u, err := url.Parse(address)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	if c.Agent != "" {
		req.Header.Set("User-Agent", c.Agent)
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{CheckRedirect: redirect}
	}
	resp, err := client.Do(req)
	if err != nil {
		// Go's own error repeats the whole address; the part after it says what went wrong.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("%s %w: %v", u.Host, ErrUnreachable, err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return resp.Body, nil
	case http.StatusNotFound:
		resp.Body.Close()
		return nil, fmt.Errorf("%s: %w", u.Path, errNotFound)
	}
	resp.Body.Close()
	// The number and not the server's own words for it, which would go to a terminal as they came.
	return nil, fmt.Errorf("%s %w: status %d", u.Host, ErrAnswer, resp.StatusCode)
}

var errNotFound = errors.New("not found")

// redirect is which redirects tty1 follows: GitHub hands a download on to another of its hosts,
// so some it must. But a request that began in private is never finished in the open.
func redirect(next *http.Request, via []*http.Request) error {
	if len(via) >= 10 || (via[0].URL.Scheme == "https" && next.URL.Scheme != "https") {
		return fmt.Errorf("a redirect tty1 will not follow, to %s://%s", next.URL.Scheme, next.URL.Host)
	}
	return nil
}

// small reads a whole small file, and refuses one that is larger than such a file can be.
func (c Client) small(ctx context.Context, address string, max int64) ([]byte, error) {
	body, err := c.get(ctx, address)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, max+1))
	if err != nil {
		return nil, fmt.Errorf("%w: the answer stopped part way: %v", ErrUnreachable, err)
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("%w: more than %d bytes where a small file was asked for", ErrAnswer, max)
	}
	return data, nil
}

// Games returns the site's list of games, as it gives them: from A to Z.
func (c Client) Games(ctx context.Context) ([]Game, error) {
	data, err := c.small(ctx, strings.TrimSuffix(c.Site, "/")+"/etc/games.json", maxList)
	if errors.Is(err, errNotFound) {
		return nil, fmt.Errorf("%w: the site has no list of games where one should be", ErrAnswer)
	}
	if err != nil {
		return nil, err
	}
	var doc struct {
		Schema int    `json:"schema"`
		Games  []Game `json:"games"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%w: the list of games: %v", ErrAnswer, err)
	}
	if doc.Schema != 1 {
		return nil, fmt.Errorf("%w: a list of games of kind %d, and this knows kind 1: a newer tty1 may read it", ErrAnswer, doc.Schema)
	}
	// The caller asks about every game it is given, so the list may not make it ask without
	// end: no more games than an arcade could hold, and each of them once.
	if len(doc.Games) > MaxGames {
		return nil, fmt.Errorf("%w: a list of %d games, and this reads %d at most", ErrAnswer, len(doc.Games), MaxGames)
	}
	seen := map[string]bool{}
	for _, g := range doc.Games {
		if !Name(g.Name) {
			return nil, fmt.Errorf("%w: a game named %q", ErrAnswer, g.Name)
		}
		if seen[g.Name] {
			return nil, fmt.Errorf("%w: a list that names %s twice", ErrAnswer, g.Name)
		}
		seen[g.Name] = true
	}
	return doc.Games, nil
}

// Name reports whether name can be a program's: lowercase letters and digits, a letter first.
// It is checked before a name goes into an address or a path.
func Name(name string) bool {
	if name == "" || len(name) > 40 || name[0] < 'a' || name[0] > 'z' {
		return false
	}
	return strings.Trim(name, "abcdefghijklmnopqrstuvwxyz0123456789") == ""
}

// release is the address of a program's releases. A game's source is private, so its releases
// are in a public repository of their own, named for its manual page (usrgames/.github#10); tty1's
// own are in tty1's repository.
func (c Client) release(name, rest string) string {
	repo := name + ".6"
	if name == "tty1" {
		repo = name
	}
	return strings.TrimSuffix(c.Releases, "/") + "/" + repo + "/releases/" + rest
}

// Newest returns the manifest of a program's newest release and its signature, unchecked. It
// answers ErrNoRelease when the program has none.
func (c Client) Newest(ctx context.Context, name string) (manifest, sig []byte, err error) {
	manifest, err = c.small(ctx, c.release(name, "latest/download/manifest.json"), maxManifest)
	if errors.Is(err, errNotFound) {
		return nil, nil, fmt.Errorf("%s %w", name, ErrNoRelease)
	}
	if err != nil {
		return nil, nil, err
	}
	sig, err = c.small(ctx, c.release(name, "latest/download/manifest.json.sig"), maxSignature)
	if errors.Is(err, errNotFound) {
		return nil, nil, fmt.Errorf("%w: the newest release of %s has a manifest and no signature", ErrAnswer, name)
	}
	if err != nil {
		return nil, nil, err
	}
	return manifest, sig, nil
}

// File opens one file of one release of a program. The caller checks what it reads against the
// manifest, and closes it.
func (c Client) File(ctx context.Context, name, version, file string) (io.ReadCloser, error) {
	body, err := c.get(ctx, c.release(name, "download/"+url.PathEscape(version)+"/"+url.PathEscape(file)))
	if errors.Is(err, errNotFound) {
		return nil, fmt.Errorf("%w: the release names %s and does not hold it", ErrAnswer, file)
	}
	return body, err
}
