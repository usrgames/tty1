package cli

import (
	"errors"
	"html"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"glonk.dev/engine"
)

func TestDoctorSaysWhatTheTerminalCanDo(t *testing.T) {
	good := engine.Terminal{IsTerminal: true, Width: 132, Height: 43, KeyReleases: true, TrueColor: true}
	with := func(change func(*engine.Terminal)) engine.Terminal {
		term := good
		change(&term)
		return term
	}
	for _, tt := range []struct {
		what     string
		terminal engine.Terminal
		says     []string
		silent   []string // what it must not say
	}{
		{"a terminal with everything", good,
			[]string{"Keys you can hold   yes", "You can walk and turn at once.", "All the colours     yes", "24-bit colour.", "A big window        132 by 43", enough},
			[]string{"Ghostty", "COLORTERM", "256", "Smaller", "only one key", "Only one key"}},
		{"one that does not report key releases", with(func(t *engine.Terminal) { t.KeyReleases = false }),
			[]string{"Keys you can hold   no", "Only one key stays held at a time. " + stillPlays, "Ghostty, kitty, WezTerm, foot, iTerm2, Alacritty, Rio, Warp.", "All the colours     yes"},
			[]string{"You can walk and turn at once", "COLORTERM"}},
		{"one that does not say it has 24-bit colour", with(func(t *engine.Terminal) { t.TrueColor = false }),
			[]string{"All the colours     256", "The games paint in the nearest of 256 colours here.", wordLost, "  export COLORTERM=truecolor", "Keys you can hold   yes"},
			[]string{"24-bit colour.", "Ghostty"}},
		{"a window too narrow", with(func(t *engine.Terminal) { t.Width = 79 }),
			[]string{"A big window        79 by 43", "Smaller than 80 columns by 24 rows. A bigger window or a smaller font is a sharper picture."},
			[]string{enough}},
		{"a window too short", with(func(t *engine.Terminal) { t.Height = 23 }),
			[]string{"A big window        132 by 23", "Smaller than 80 columns by 24 rows."},
			[]string{enough}},
		{"a window of just the size that is enough", with(func(t *engine.Terminal) { t.Width, t.Height = 80, 24 }),
			[]string{"A big window        80 by 24", enough},
			[]string{"Smaller"}},
		{"a terminal that gives no size", with(func(t *engine.Terminal) { t.Width, t.Height = 0, 0 }),
			[]string{"A big window        not said", "This terminal did not say its size. The games take it as 80 by 24."},
			[]string{"0 by 0", "Smaller", enough}},
		{"a terminal with none of it", engine.Terminal{IsTerminal: true, Width: 72, Height: 20},
			[]string{"Keys you can hold   no", "All the colours     256", "A big window        72 by 20", "Smaller than"},
			[]string{"yes"}},
	} {
		w := newWorld(t)
		w.app.Probe = func() (engine.Terminal, error) { return tt.terminal, nil }
		// A terminal that plays the games less well is no failure: doctor fails only where a
		// game could not run at all.
		status, out, errs := w.run("doctor")
		if status != 0 || errs != "" {
			t.Errorf("%s: wanted status 0 and nothing on the error stream, got %d: %s", tt.what, status, errs)
		}
		for _, want := range tt.says {
			if !strings.Contains(out, want) {
				t.Errorf("%s: wanted it to say %q, got:\n%s", tt.what, want, out)
			}
		}
		for _, not := range tt.silent {
			if strings.Contains(out, not) {
				t.Errorf("%s: wanted it not to say %q, got:\n%s", tt.what, not, out)
			}
		}
	}
}

func TestDoctorFailsOnlyWhereAGameCouldNotRun(t *testing.T) {
	w := newWorld(t)
	w.app.Probe = func() (engine.Terminal, error) { return engine.Terminal{}, nil }
	if status, out, errs := w.run("doctor"); status != 1 || out != "" || !strings.Contains(errs, "has to run in a terminal") {
		t.Errorf("with no terminal: wanted status 1 and the reason, and no report, got %d: %q %q", status, out, errs)
	}
	w.app.Probe = func() (engine.Terminal, error) { return engine.Terminal{}, errors.New("the terminal said no") }
	if status, out, errs := w.run("doctor"); status != 1 || out != "" || !strings.Contains(errs, "the terminal said no") {
		t.Errorf("with a terminal that could not be asked: wanted status 1 and the reason, got %d: %q %q", status, out, errs)
	}

	asked := 0
	w.app.Probe = func() (engine.Terminal, error) { asked++; return engine.Terminal{IsTerminal: true}, nil }
	if status, _, errs := w.run("doctor", "rustwake"); status != 2 || asked != 0 || !strings.Contains(errs, "takes nothing after it") {
		t.Errorf("doctor with a word after it: wanted status 2 and the terminal left alone, got %d after %d questions: %s", status, asked, errs)
	}
	if _, out, _ := w.run("help"); !strings.Contains(out, "tty1 doctor") {
		t.Errorf("wanted the help to list doctor, got:\n%s", out)
	}
}

// The page /terminals of usr.games and tty1 doctor tell a player the same things, so they must use
// the same words: every sentence doctor borrows is on the page, and the terminals it names are the
// ones the page names, in the page's order.
//
// The page is read from the file TTY1_TERMINALS_PAGE names, or from a clone of the site beside
// this repository. With neither the test skips itself, so CI fetches the page and requires this
// test to have run.
func TestItsWordsAreThePagesWords(t *testing.T) {
	path := os.Getenv("TTY1_TERMINALS_PAGE")
	if path == "" {
		path = "../../../usr.games/site/terminals.html"
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("no page to hold the words to: %v", err)
	}
	// The page's text, as a reader has it: no tags, one space between words.
	text := html.UnescapeString(regexp.MustCompile(`<[^>]*>`).ReplaceAllString(string(raw), " "))
	text = strings.ToLower(strings.Join(strings.Fields(text), " "))

	for _, words := range []string{keysTitle, coloursTitle, windowTitle, walkAndTurn, oneKey, stillPlays, trueColour, nearest256, wordLost, setColour, enough, sharper} {
		if !strings.Contains(text, strings.ToLower(words)) {
			t.Errorf("doctor says %q, and the page does not", words)
		}
	}

	_, after, found := strings.Cut(text, "tell it:")
	names, _, ended := strings.Cut(after, "in the others")
	if !found || !ended {
		t.Fatal("the page no longer names its terminals between \"tell it:\" and \"In the others\": find them again")
	}
	var mine []string
	for _, name := range keyTerminals {
		mine = append(mine, strings.ToLower(name))
	}
	if theirs := strings.Fields(names); !slices.Equal(theirs, mine) {
		t.Errorf("the page names %v as reporting key releases, and doctor names %v", theirs, mine)
	}
}
