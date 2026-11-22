package shelf

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// stage makes a staged game: a program and a licence, as an archive unpacks.
func stage(t *testing.T, s Shelf, name, body string) string {
	t.Helper()
	dir, err := s.Stage()
	if err != nil {
		t.Fatal(err)
	}
	for file, text := range map[string]string{name: body, "LICENSE": "MIT\n"} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(text), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func manifest(version string) []byte {
	return []byte(`{"schema": 1, "name": "hello", "version": "` + version + `", "files": []}`)
}

func TestAGameIsPutOnTheShelfAndReplacedWhole(t *testing.T) {
	s, err := At(filepath.Join(t.TempDir(), "not", "made", "yet"))
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := s.Version("hello"); ok {
		t.Fatalf("wanted nothing installed on a new shelf, got %s", v)
	}
	if _, ok := s.Program("hello"); ok || len(s.Installed()) != 0 {
		t.Fatal("wanted no program and no games on a new shelf")
	}

	if err := s.Put("hello", stage(t, s, "hello", "the first"), manifest("v1.0.0")); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.Version("hello"); v != "v1.0.0" {
		t.Errorf("wanted v1.0.0 installed, got %q", v)
	}
	path, ok := s.Program("hello")
	if got, _ := os.ReadFile(path); !ok || string(got) != "the first" {
		t.Errorf("wanted the program as staged, got %q", got)
	}

	// A file that only the first one had must not outlive it.
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "left-over"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Put("hello", stage(t, s, "hello", "the second"), manifest("v1.1.0")); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.Version("hello"); v != "v1.1.0" {
		t.Errorf("wanted v1.1.0 installed, got %q", v)
	}
	if got, _ := os.ReadFile(path); string(got) != "the second" {
		t.Errorf("wanted the second program, got %q", got)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "left-over")); err == nil {
		t.Error("wanted nothing of the first one left in the game's folder")
	}
	if left, _ := os.ReadDir(filepath.Join(s.Dir(), "staging")); len(left) != 0 {
		t.Errorf("wanted nothing left in staging, got %d entries", len(left))
	}
	if got := s.Installed(); !slices.Equal(got, []string{"hello"}) {
		t.Errorf("wanted hello installed, got %v", got)
	}
}

// What was unpacked has to hold the program it is installed as. If it does not, the game that
// was there stays as it was.
func TestAFolderWithoutItsProgramIsNotPut(t *testing.T) {
	s, _ := At(t.TempDir())
	if err := s.Put("hello", stage(t, s, "hello", "the first"), manifest("v1.0.0")); err != nil {
		t.Fatal(err)
	}
	if err := s.Put("hello", stage(t, s, "goodbye", "another program"), manifest("v2.0.0")); err == nil {
		t.Fatal("wanted a folder with no program named hello refused")
	}
	if v, _ := s.Version("hello"); v != "v1.0.0" {
		t.Errorf("wanted v1.0.0 still installed, got %q", v)
	}
	path, _ := s.Program("hello")
	if got, _ := os.ReadFile(path); string(got) != "the first" {
		t.Errorf("wanted the first program untouched, got %q", got)
	}
}

func TestOnlyAWholeGameCountsAsInstalled(t *testing.T) {
	s, _ := At(t.TempDir())
	games := filepath.Join(s.Dir(), "games")
	// A folder with a program and no manifest, one with a manifest and no program, one whose
	// manifest names no version, and a stray file.
	for path, text := range map[string]string{
		"noman/noman":          "x",
		"noprog/manifest.json": string(manifest("v1.0.0")),
		"nover/nover":          "x",
		"nover/manifest.json":  `{"schema": 1}`,
		"stray":                "x",
	} {
		full := filepath.Join(games, path)
		os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(text), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if got := s.Installed(); len(got) != 0 {
		t.Errorf("wanted none of them to count as installed, got %v", got)
	}
}

func TestTheShelfIsWhereThePlayerSaysOrUnderTheirHome(t *testing.T) {
	home, _ := os.UserHomeDir()
	for _, tt := range []struct {
		what string
		env  map[string]string
		want string
	}{
		{"nothing said", nil, filepath.Join(home, ".local", "share", "tty1")},
		{"XDG_DATA_HOME", map[string]string{"XDG_DATA_HOME": "/data"}, "/data/tty1"},
		{"an XDG_DATA_HOME that is no full path, which the standard says to ignore", map[string]string{"XDG_DATA_HOME": "data"}, filepath.Join(home, ".local", "share", "tty1")},
		{"TTY1_HOME, over everything", map[string]string{"TTY1_HOME": "/games", "XDG_DATA_HOME": "/data"}, "/games"},
	} {
		s, err := Find(func(k string) string { return tt.env[k] })
		if err != nil || s.Dir() != tt.want {
			t.Errorf("%s: wanted %s, got %s and %v", tt.what, tt.want, s.Dir(), err)
		}
	}
}
