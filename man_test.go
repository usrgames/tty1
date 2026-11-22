//go:build linux || darwin

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// standInCat returns a folder holding a man that prints the file it is handed, with the shell's
// own words only: the folder is the whole PATH, so there is no cat for it to call.
func standInCat(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nwhile IFS= read -r line || [ -n \"$line\" ]; do printf '%s\\n' \"$line\"; done < \"$1\"\n"
	if err := os.WriteFile(filepath.Join(dir, "man"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The manual page and the help are two accounts of one program, so a command in one is in the
// other. The page also says where tty1 writes, what it reads from the environment, and what its
// exit statuses mean: a page that lost one of those is caught here.
func TestThePageNamesEveryCommandTheHelpNames(t *testing.T) {
	help, err := tty1(nil, "help").Output()
	if err != nil {
		t.Fatal(err)
	}
	// In a manual page's source a hyphen that is to stay a hyphen is written \-.
	page := strings.ReplaceAll(string(manual), `\-`, "-")
	// A command is in the page twice: once in the synopsis, once where it is described. The two
	// parts are read apart, since the same line in one would otherwise stand in for the other.
	_, rest, _ := strings.Cut(page, ".SH SYNOPSIS\n")
	synopsis, rest, _ := strings.Cut(rest, ".SH DESCRIPTION\n")
	description, _, found := strings.Cut(rest, ".SH ")
	if !found || synopsis == "" || description == "" {
		t.Fatal("the page no longer has a synopsis, then a description, then more: find them again")
	}
	commands := 0
	for _, line := range strings.Split(string(help), "\n") {
		words := strings.Fields(line)
		if !strings.HasPrefix(line, "  tty1 ") || len(words) < 2 {
			continue
		}
		commands++
		if !strings.Contains(synopsis, ".B tty1 "+words[1]+"\n") {
			t.Errorf("the help names `tty1 %s`, and the page's synopsis does not", words[1])
		}
		if !strings.Contains(description, "tty1 "+words[1]) {
			t.Errorf("the help names `tty1 %s`, and the page does not describe it", words[1])
		}
	}
	if commands < 8 {
		t.Fatalf("found only %d commands in the help, which this test reads line by line: has its layout changed?", commands)
	}
	for _, want := range []string{
		".SH FILES", "~/.local/share/tty1/games/", "~/.local/share/tty1/staging/", "~/.local/share/tty1/man/tty1.1",
		".SH ENVIRONMENT", ".B TTY1_HOME", ".B TTY1_SITE", ".B TTY1_RELEASES", ".B XDG_DATA_HOME",
		".SH EXIT STATUS", ".B 0\n", ".B 1\n", ".B 2\n",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("wanted the page to hold %q", want)
		}
	}
}

// The real program shows the page that is built into it: with a stand-in man first on the PATH,
// `tty1 man` hands it a file that holds man/tty1.1 as it is in this repository.
func TestTheRealProgramCarriesItsPage(t *testing.T) {
	home := t.TempDir()
	cmd := tty1([]string{"TTY1_HOME=" + home, "PATH=" + standInCat(t)}, "man")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("tty1 man: %v", err)
	}
	if string(out) != string(manual) {
		t.Errorf("wanted man handed the page as it is written, %d bytes, got %d bytes", len(manual), len(out))
	}
}
