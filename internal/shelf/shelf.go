// Package shelf is where installed games live: one folder of tty1's own, with a folder for each
// game holding what its archive held and the manifest that vouched for it. Nothing is put on the
// player's PATH, so removing this one folder removes everything tty1 ever installed.
package shelf

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Shelf is the folder of installed games.
type Shelf struct{ dir string }

// Find returns the shelf's place: $TTY1_HOME if it is set, else tty1 under $XDG_DATA_HOME, else
// ~/.local/share/tty1, on a Mac as on Linux, since that is where a terminal's programs keep theirs.
func Find(getenv func(string) string) (Shelf, error) {
	if dir := getenv("TTY1_HOME"); dir != "" {
		return At(dir)
	}
	if dir := getenv("XDG_DATA_HOME"); filepath.IsAbs(dir) {
		return At(filepath.Join(dir, "tty1"))
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Shelf{}, fmt.Errorf("tty1 keeps its games under your home folder, and could not find it: %w", err)
	}
	return At(filepath.Join(home, ".local", "share", "tty1"))
}

// At returns the shelf in the folder dir, which need not exist yet.
func At(dir string) (Shelf, error) {
	abs, err := filepath.Abs(dir)
	return Shelf{abs}, err
}

// Dir returns the folder everything is kept in.
func (s Shelf) Dir() string { return s.dir }

func (s Shelf) game(name string) string { return filepath.Join(s.dir, "games", name) }

// Version returns the version of the game that is installed, as the manifest it was installed
// with says. This manifest was checked when it was put here and is read as it was left.
func (s Shelf) Version(name string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(s.game(name), "manifest.json"))
	if err != nil {
		return "", false
	}
	var m struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(data, &m) != nil || m.Version == "" {
		return "", false
	}
	return m.Version, true
}

// Program returns the path of an installed game's program.
func (s Shelf) Program(name string) (string, bool) {
	path := filepath.Join(s.game(name), name)
	info, err := os.Stat(path)
	if _, installed := s.Version(name); !installed || err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	return path, true
}

// Manual returns the path of an installed game's manual page, which its archive held as NAME.6:
// the studio files its games in section 6 of the manual.
func (s Shelf) Manual(name string) (string, bool) {
	path := filepath.Join(s.game(name), name+".6")
	info, err := os.Stat(path)
	if _, installed := s.Program(name); !installed || err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	return path, true
}

// Installed returns the names of the games that are installed, from A to Z.
func (s Shelf) Installed() []string {
	entries, _ := os.ReadDir(filepath.Join(s.dir, "games"))
	var names []string
	for _, e := range entries {
		if _, ok := s.Program(e.Name()); ok {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// Stage returns a new, empty folder to unpack a game into, on the same disk as the shelf so
// that Put can move it into place in one step. Whoever asks for it removes it if it is not Put.
func (s Shelf) Stage() (string, error) {
	staging := filepath.Join(s.dir, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return "", err
	}
	return os.MkdirTemp(staging, "game-")
}

// Put makes the staged folder the installed game name, with the manifest that vouched for what
// is in it, in place of whatever was installed. The game is whole at every moment but one: the
// old folder is moved aside, the new one moved in, and only then is the old one removed. If the
// new one cannot be moved in, the old one is moved back.
func (s Shelf) Put(name, staged string, manifest []byte) error {
	if info, err := os.Stat(filepath.Join(staged, name)); err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("the archive holds no program named %s", name)
	}
	if err := os.WriteFile(filepath.Join(staged, "manifest.json"), manifest, 0o644); err != nil {
		return err
	}
	dst := s.game(name)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return swap(os.Rename, staged, dst)
}

// swap moves the folder staged to dst, in the stead of what is there. The move is handed in so
// that a test can make it fail part way, which a real disk will not do on request.
func swap(move func(from, to string) error, staged, dst string) error {
	aside := staged + ".old"
	had := true
	if err := move(dst, aside); errors.Is(err, os.ErrNotExist) {
		had = false
	} else if err != nil {
		return err
	}
	if err := move(staged, dst); err != nil {
		if had {
			move(aside, dst)
		}
		return err
	}
	if had {
		os.RemoveAll(aside)
	}
	return nil
}
