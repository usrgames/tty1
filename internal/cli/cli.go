// Package cli is tty1's commands: ls, install, update, run, doctor and man. The first four are a few
// steps over the packages beside this one, in an order that matters: a release is checked before anything of it
// is unpacked, unpacked before anything of it is put in place, and put in place whole.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"text/tabwriter"
	"time"
	"unicode"

	"glonk.dev/engine"

	"usr.games/tty1/internal/fetch"
	"usr.games/tty1/internal/release"
	"usr.games/tty1/internal/shelf"
	"usr.games/tty1/internal/unpack"
	"usr.games/tty1/internal/version"
)

// App is tty1, with everything it touches handed to it, so that a test can hand it a shelf in a
// temporary folder, a network that is one local server, and a studio made for the test.
type App struct {
	Out, Err io.Writer
	Shelf    shelf.Shelf
	Net      fetch.Client
	Signers  release.Signers
	Now      func() time.Time
	OS, Arch string // what this machine is, as a release names it: darwin or linux, arm64 or amd64

	Version  string // this tty1's own version
	Released bool   // whether it was built by a release, and not by `go install` or by hand
	Self     string // the file of the running program, which an update replaces

	// Exec starts a game in this process's place, so that the game has the terminal to itself.
	Exec func(program string, args []string) error

	// Probe asks the terminal tty1 is running in what it can do, as a game would at its start.
	Probe func() (engine.Terminal, error)

	// Manual is tty1's own manual page, as it is written in man/tty1.1.
	Manual []byte
}

// Names no game may have: they are the studio's own programs, which install another way. The
// studio signs these with the key it signs games with, so nothing but this list keeps one from
// being installed as a game: a program of the studio's that is no game is added here.
var reserved = map[string]bool{"tty1": true, "arcade": true, "relay": true}

// atOnce is how many games ls asks about at the same time.
const atOnce = 8

const usage = `tty1 is the usr.games launcher: it brings the games home, keeps them current, and starts them.

  tty1 ls                 list the games
  tty1 install NAME       bring a game home
  tty1 update [NAME]      bring everything up to date, or one game
  tty1 run NAME [...]     start a game
  tty1 doctor             say what this terminal can do
  tty1 man [NAME]         show the manual page of a game, or this program's
  tty1 help               show this
  tty1 --version          say which tty1 this is

A release is checked against the studio's signature before anything of it is unpacked.
`

// Run does what the command line asks and returns the status to exit with: 0 when it was done,
// 1 when it could not be, 2 when the command line was not understood.
func (a *App) Run(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(a.Out, usage)
		return 0
	}
	switch cmd, rest := args[0], args[1:]; cmd {
	case "help", "-h", "--help":
		fmt.Fprint(a.Out, usage)
		return 0
	case "version", "--version":
		fmt.Fprintln(a.Out, "tty1", a.Version)
		return 0
	case "ls":
		if len(rest) != 0 {
			return a.misuse("tty1 ls takes nothing after it")
		}
		return a.ls(ctx)
	case "install":
		if len(rest) != 1 {
			return a.misuse("tty1 install takes one game, as in: tty1 install rustwake")
		}
		return a.status(a.install(ctx, rest[0], false))
	case "update":
		if len(rest) > 1 {
			return a.misuse("tty1 update takes one game, or none for everything")
		}
		return a.update(ctx, rest)
	case "run":
		if len(rest) == 0 {
			return a.misuse("tty1 run takes a game, as in: tty1 run rustwake")
		}
		return a.run(rest[0], rest[1:])
	case "man":
		if len(rest) > 1 {
			return a.misuse("tty1 man takes one game, or none for tty1's own page")
		}
		return a.status(a.man(rest))
	case "doctor":
		if len(rest) != 0 {
			return a.misuse("tty1 doctor takes nothing after it")
		}
		return a.doctor()
	default:
		if fetch.Name(cmd) {
			return a.misuse(fmt.Sprintf("tty1 does not know %q. To start a game: tty1 run %s", cmd, cmd))
		}
		return a.misuse(fmt.Sprintf("tty1 does not know %q", cmd))
	}
}

func (a *App) misuse(what string) int {
	fmt.Fprintln(a.Err, what)
	fmt.Fprintln(a.Err, "tty1 help shows what it can do.")
	return 2
}

// status prints why something could not be done, if it could not, and returns the exit status.
func (a *App) status(err error) int {
	if err != nil {
		// An error can carry what the network said: the name in a certificate, the address of
		// a redirect. It goes to a terminal as words, like everything else that was fetched.
		fmt.Fprintln(a.Err, Clean(err.Error()))
		return 1
	}
	return 0
}

// installed returns the installed games whose folders have a game's name. A folder of any other
// name on the shelf was not put there by tty1, and is neither printed nor asked about.
func (a *App) installed() []string {
	var names []string
	for _, name := range a.Shelf.Installed() {
		if fetch.Name(name) {
			names = append(names, name)
		}
	}
	return names
}

// newest asks for a program's newest release and checks it. The manifest it returns is one the
// studio signed for that program, as of now, and the bytes are the manifest as it was signed.
func (a *App) newest(ctx context.Context, name string) (release.Manifest, version.V, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	raw, sig, err := a.Net.Newest(ctx, name)
	if err != nil {
		return release.Manifest{}, version.V{}, nil, err
	}
	m, err := a.Signers.Newest(raw, sig, name, a.Now())
	if err != nil {
		return release.Manifest{}, version.V{}, nil, fmt.Errorf("the newest release of %s does not check out, so nothing of it was touched: %w", name, err)
	}
	v, err := version.Parse(m.Version)
	if err != nil {
		return release.Manifest{}, version.V{}, nil, fmt.Errorf("the newest release of %s: %w", name, err)
	}
	return m, v, raw, nil
}

func (a *App) ls(ctx context.Context) int {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	games, err := a.Net.Games(ctx)
	if err != nil {
		// With no list, what is installed is still worth showing: that needs no network.
		fmt.Fprintln(a.Err, "The list of games could not be read:", Clean(err.Error()))
		installed := a.installed()
		if len(installed) == 0 {
			return 1
		}
		fmt.Fprintln(a.Err, "Here is what is installed.")
		for _, name := range installed {
			games = append(games, fetch.Game{Name: name})
		}
	}
	listed := map[string]bool{}
	for _, g := range games {
		listed[g.Name] = true
	}
	for _, name := range a.installed() {
		if !listed[name] {
			games = append(games, fetch.Game{Name: name, Blurb: "(no longer on the list)"})
		}
	}

	// Each game's newest release is asked for at once: a slow answer for one holds up none.
	states := make([]string, len(games))
	slots := make(chan struct{}, atOnce)
	var wg sync.WaitGroup
	for i, g := range games {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// A few at a time: the list decides how many games there are, and it is not trusted.
			slots <- struct{}{}
			defer func() { <-slots }()
			states[i] = a.state(ctx, g.Name)
		}()
	}
	wg.Wait()

	w := tabwriter.NewWriter(a.Out, 0, 0, 2, ' ', 0)
	for i, g := range games {
		// The list is the site's word, not the studio's signature: it is printed as words only.
		fmt.Fprintf(w, "%s(6)\t%s\t%s\t%s\n", g.Name, Clean(g.Players), Clean(states[i]), Clean(g.Blurb))
	}
	w.Flush()
	if len(games) == 0 {
		fmt.Fprintln(a.Out, "No games are listed yet.")
	}
	return 0
}

// state says in a few words where a game stands: installed or not, and what is newer.
func (a *App) state(ctx context.Context, name string) string {
	have, installed := a.Shelf.Version(name)
	_, newest, _, err := a.newest(ctx, name)
	switch {
	case errors.Is(err, fetch.ErrNoRelease) && installed:
		return have
	case errors.Is(err, fetch.ErrNoRelease):
		return "no release yet"
	case err != nil && installed:
		return have + ", newest unknown"
	case err != nil:
		return "not installed, newest unknown"
	case !installed:
		return "not installed, " + newest.String() + " is out"
	}
	if old, perr := version.Parse(have); perr == nil && newest.Compare(old) > 0 {
		return have + ", " + newest.String() + " is out"
	}
	return have
}

// install brings a game's newest release home. With mustHave it is an update: a game that is
// not installed is not brought home by being asked to be kept current.
func (a *App) install(ctx context.Context, name string, mustHave bool) error {
	switch {
	case !fetch.Name(name):
		return fmt.Errorf("%q is not the name of a game. tty1 ls lists them", name)
	case reserved[name]:
		return fmt.Errorf("%s is not a game. tty1 update keeps tty1 itself current", name)
	}
	have, installed := a.Shelf.Version(name)
	if mustHave && !installed {
		return fmt.Errorf("%s is not installed. Bring it home with: tty1 install %s", name, name)
	}
	m, newest, raw, err := a.newest(ctx, name)
	if errors.Is(err, fetch.ErrNoRelease) {
		return fmt.Errorf("%s has no release yet, so there is nothing to bring home", name)
	}
	if err != nil {
		return err
	}
	if installed {
		old, err := version.Parse(have)
		switch {
		case err != nil:
			// What is on the shelf cannot be placed: the newest release replaces it.
		case newest.Compare(old) < 0:
			// An older release where the newest should be is what a release put back looks like.
			return fmt.Errorf("the newest release of %s is %s, older than the %s installed: tty1 does not go back, and left it as it is", name, newest, have)
		case newest.Compare(old) == 0:
			fmt.Fprintf(a.Out, "%s %s is already here.\n", name, have)
			return nil
		}
	}

	stage, err := a.fetch(ctx, name, m)
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := a.Shelf.Put(name, stage, raw); err != nil {
		return fmt.Errorf("%s %s could not be put in place: %w", name, newest, err)
	}
	if installed {
		fmt.Fprintf(a.Out, "%s is now %s, up from %s.\n", name, newest, Clean(have))
	} else {
		fmt.Fprintf(a.Out, "%s %s is home. Start it with: tty1 run %s\n", name, newest, name)
	}
	return nil
}

// fetch downloads the archive of a checked release for this machine and unpacks it into a new
// folder, which it returns for the caller to put in place and to remove. The order is the whole
// point: the archive is first read to its end into a file while its size and hash are checked
// against the manifest, and only an archive that passed is unpacked.
func (a *App) fetch(ctx context.Context, name string, m release.Manifest) (string, error) {
	file, ok := m.For(a.OS, a.Arch)
	if !ok {
		return "", fmt.Errorf("%s %s has no build for %s on %s", name, m.Version, a.OS, a.Arch)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	stage, err := a.Shelf.Stage()
	if err != nil {
		return "", err
	}
	done := false
	defer func() {
		if !done {
			os.RemoveAll(stage)
		}
	}()

	// Beside the folder, not in it: the archive is no part of what is installed.
	archive, err := os.CreateTemp(filepath.Dir(stage), "archive-")
	if err != nil {
		return "", err
	}
	defer os.Remove(archive.Name())
	defer archive.Close()

	body, err := a.Net.File(ctx, name, m.Version, file.Name)
	if err != nil {
		return "", err
	}
	err = m.Check(file.Name, io.TeeReader(body, archive))
	body.Close()
	if err != nil {
		return "", fmt.Errorf("%s %s was not installed: what was downloaded is not what the release names: %w", name, m.Version, err)
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	if _, err := unpack.Into(stage, archive); err != nil {
		return "", fmt.Errorf("%s %s was not installed: %w", name, m.Version, err)
	}
	done = true
	return stage, nil
}

func (a *App) update(ctx context.Context, rest []string) int {
	if len(rest) == 1 {
		return a.status(a.install(ctx, rest[0], true))
	}
	failed := false
	for _, name := range a.installed() {
		if a.status(a.install(ctx, name, true)) != 0 {
			failed = true
		}
	}
	if a.status(a.updateSelf(ctx)) != 0 {
		failed = true
	}
	if failed {
		return 1
	}
	return 0
}

// updateSelf replaces the running tty1 with its newest release. The new program is checked and
// unpacked like a game, written out beside the old one, and moved over it in one step: at no
// moment is there half a tty1.
func (a *App) updateSelf(ctx context.Context) error {
	if !a.Released {
		fmt.Fprintf(a.Out, "This tty1 (%s) was not installed from a release, so it does not replace itself. Update it the way you got it, such as: go install usr.games/tty1@latest\n", a.Version)
		return nil
	}
	current, err := version.Parse(a.Version)
	if err != nil {
		return fmt.Errorf("tty1 cannot tell its own version, %q, so it will not replace itself", a.Version)
	}
	m, newest, _, err := a.newest(ctx, "tty1")
	if errors.Is(err, fetch.ErrNoRelease) {
		return nil
	}
	if err != nil {
		return err
	}
	switch c := newest.Compare(current); {
	case c < 0:
		return fmt.Errorf("the newest release of tty1 is %s, older than this %s: tty1 does not go back, and left itself as it is", newest, current)
	case c == 0:
		fmt.Fprintf(a.Out, "tty1 %s is current.\n", current)
		return nil
	}
	stage, err := a.fetch(ctx, "tty1", m)
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	program, err := os.ReadFile(filepath.Join(stage, "tty1"))
	if err != nil {
		return fmt.Errorf("tty1 %s was not installed: its archive holds no tty1", newest)
	}
	// A new file of a name nobody could have guessed, never one that may already be there: a
	// link left beside tty1 would otherwise be written through, and tty1 would become that link.
	beside, err := os.CreateTemp(filepath.Dir(a.Self), ".tty1-new-")
	if err != nil {
		return fmt.Errorf("tty1 could not write beside itself, at %s, so it left itself as it is: %w", a.Self, err)
	}
	defer os.Remove(beside.Name())
	_, err = beside.Write(program)
	if cerr := beside.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(beside.Name(), 0o755)
	}
	if err == nil {
		err = os.Rename(beside.Name(), a.Self)
	}
	if err != nil {
		return fmt.Errorf("tty1 could not replace itself at %s, so it left itself as it is: %w", a.Self, err)
	}
	fmt.Fprintf(a.Out, "tty1 is now %s, up from %s.\n", newest, current)
	return nil
}

func (a *App) run(name string, args []string) int {
	if !fetch.Name(name) {
		return a.status(fmt.Errorf("%q is not the name of a game. tty1 ls lists them", name))
	}
	program, ok := a.Shelf.Program(name)
	if !ok {
		return a.status(fmt.Errorf("%s is not installed. Bring it home with: tty1 install %s", name, name))
	}
	if err := a.Exec(program, append([]string{name}, args...)); err != nil {
		return a.status(fmt.Errorf("%s could not be started: %w", name, err))
	}
	return 0
}

// Clean returns s with nothing in it that could move a terminal's cursor or change its colours:
// what a server says is printed for a person, and must stay words.
func Clean(s string) string {
	return strings.Map(func(r rune) rune {
		// Every control character, the ones above 0x7f among them, which some terminals take
		// as the start of a command just as they take ESC. And the marks that turn the
		// direction of text around, or break a line, which let words be shown as other words.
		if unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) || r == '\u2028' || r == '\u2029' {
			return -1
		}
		return r
	}, s)
}
