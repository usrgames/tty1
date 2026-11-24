// Command tty1 is the usr.games launcher: it brings the games home, keeps them current, and
// starts them.
//
//	tty1 ls
//	tty1 install rustwake
//	tty1 run rustwake
//	tty1 doctor
//	tty1 man rustwake
//
// It asks the site for the list of games and each game's repository for its newest release,
// checks a release against the studio's signature before anything of it is unpacked, and keeps
// what it installs in one folder of its own, ~/.local/share/tty1. Three variables point it
// elsewhere, for trying it out: TTY1_HOME for that folder, TTY1_SITE for the site, and
// TTY1_RELEASES for where the release repositories are. None of them changes whose signature it
// accepts.
package main

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"syscall"
	"time"

	"glonk.dev/engine"

	"usr.games/tty1/internal/cli"
	"usr.games/tty1/internal/fetch"
	"usr.games/tty1/internal/release"
	"usr.games/tty1/internal/shelf"
)

// version is set when a release is built. A tty1 built any other way has "dev" here.
var version = "dev"

// The manual page is built into the program, so that `tty1 man` has it however tty1 was installed.
//
//go:embed man/tty1.1
var manual []byte

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	games, err := shelf.Find(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// Built by `go install`, tty1 still knows which version it is, from what Go recorded. It
	// only replaces itself when a release built it: otherwise it is someone else's to update.
	v, released := version, version != "dev"
	if info, ok := debug.ReadBuildInfo(); !released && ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		v = info.Main.Version
	}
	self, err := os.Executable()
	if err == nil {
		self, err = filepath.EvalSymlinks(self)
	}
	if err != nil {
		released = false
	}
	or := func(key, otherwise string) string {
		if v := os.Getenv(key); v != "" {
			return v
		}
		return otherwise
	}
	app := &cli.App{
		Out: os.Stdout, Err: os.Stderr,
		Shelf:   games,
		Net:     fetch.Client{Site: or("TTY1_SITE", fetch.Site), Releases: or("TTY1_RELEASES", fetch.Releases), Agent: "tty1/" + v},
		Signers: release.Studio(),
		Now:     time.Now,
		OS:      runtime.GOOS, Arch: runtime.GOARCH,
		Version: v, Released: released, Self: self,
		// The game takes this process's place: it gets the terminal, the signals and the exit
		// status with no tty1 left standing between.
		Exec: func(program string, args []string) error { return syscall.Exec(program, args, os.Environ()) },
		// The one thing tty1 asks of glonk: the question a game asks its terminal as it starts.
		Probe:  func() (engine.Terminal, error) { return engine.Probe(engine.Config{}) },
		Manual: manual,
	}
	os.Exit(app.Run(ctx, os.Args[1:]))
}
