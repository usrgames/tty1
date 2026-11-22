package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"usr.games/tty1/internal/fetch"
)

// man shows a manual page with the machine's own man, which takes tty1's place as a game does:
// the page an installed game's archive held, or tty1's own, which is built into the program.
// It needs no network.
func (a *App) man(rest []string) error {
	name := "tty1"
	if len(rest) == 1 {
		name = rest[0]
	}
	var page string
	switch {
	case !fetch.Name(name):
		return fmt.Errorf("%q is not the name of a game. tty1 ls lists them", name)
	case name == "tty1":
		var err error
		if page, err = a.ownPage(); err != nil {
			return fmt.Errorf("tty1 could not write its manual page where man can read it: %w", err)
		}
	case reserved[name]:
		return fmt.Errorf("%s is not a game, and tty1 holds no page for it", name)
	default:
		if _, installed := a.Shelf.Program(name); !installed {
			return fmt.Errorf("%s is not installed, and its manual page comes with it. Bring it home with: tty1 install %s", name, name)
		}
		var ok bool
		if page, ok = a.Shelf.Manual(name); !ok {
			return fmt.Errorf("%s came without a manual page", name)
		}
	}
	man, err := exec.LookPath("man")
	if err != nil {
		return fmt.Errorf("this machine has no man to show a page with. The page is the file %s", page)
	}
	if err := a.Exec(man, []string{"man", page}); err != nil {
		return fmt.Errorf("man could not be started: %w", err)
	}
	return nil
}

// ownPage writes tty1's manual page into its folder, since man reads a page from a file, and
// returns where. It is written beside and moved into place, so that whatever is at that path,
// a link included, is replaced and never written through.
func (a *App) ownPage() (string, error) {
	dir := filepath.Join(a.Shelf.Dir(), "man")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, "page-")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(a.Manual)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), 0o644)
	}
	page := filepath.Join(dir, "tty1.1")
	if err == nil {
		err = os.Rename(tmp.Name(), page)
	}
	return page, err
}
