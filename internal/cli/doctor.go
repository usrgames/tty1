package cli

import (
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"
	"unicode"
)

// The words doctor shares with the page /terminals of usr.games. A test holds every one of them
// to that page, so that the command and the page never say different things.
const (
	keysTitle    = "Keys you can hold"
	coloursTitle = "All the colours"
	windowTitle  = "A big window"

	walkAndTurn = "walk and turn at once"
	oneKey      = "only one key stays held at a time"
	stillPlays  = "Everything still plays. You just cannot walk and turn together."
	trueColour  = "24-bit colour"
	nearest256  = "the nearest of 256 colours"
	wordLost    = "Inside tmux or over ssh, the terminal's word often does not arrive. If the colours look banded there, this brings them back:"
	setColour   = "export COLORTERM=truecolor"
	enough      = "80 columns by 24 rows is enough to play. More is better."
	sharper     = "a bigger window or a smaller font is a sharper picture"
)

// The size the page calls enough to play.
const enoughCols, enoughRows = 80, 24

// The terminals the page names as telling a game when a key is let go.
var keyTerminals = []string{"Ghostty", "kitty", "WezTerm", "foot", "iTerm2", "Alacritty", "Rio", "Warp"}

// doctor looks at the terminal tty1 is running in and says what it found: what a game can do
// here, and what to change where something can be. It fails only where a game could not run at
// all. A terminal that plays the games less well is told so, and that is no failure.
func (a *App) doctor() int {
	t, err := a.Probe()
	if err != nil {
		return a.status(fmt.Errorf("tty1 could not ask this terminal what it can do: %w", err))
	}
	if !t.IsTerminal {
		return a.status(errors.New("tty1 doctor has to run in a terminal, and its input or its output is not one: a game could not run here either"))
	}

	w := tabwriter.NewWriter(a.Out, 0, 0, 3, ' ', 0)
	row := func(title, found string, says ...string) {
		for i, line := range says {
			if i > 0 {
				title, found = "", ""
			}
			fmt.Fprintf(w, "%s\t%s\t%s\n", title, found, line)
		}
	}

	if t.KeyReleases {
		row(keysTitle, "yes", "You can "+walkAndTurn+".")
	} else {
		row(keysTitle, "no",
			sentence(oneKey)+". "+stillPlays,
			"These terminals tell a game when you let go of a key: "+strings.Join(keyTerminals, ", ")+".")
	}

	if t.TrueColor {
		row(coloursTitle, "yes", sentence(trueColour)+".")
	} else {
		row(coloursTitle, "256", "The games paint in "+nearest256+" here.", wordLost, "  "+setColour)
	}

	switch size := fmt.Sprintf("%d by %d", t.Width, t.Height); {
	case t.Width <= 0 || t.Height <= 0:
		// glonk takes a terminal that reports no size as 80 by 24, so the games do too.
		row(windowTitle, "not said", "This terminal did not say its size. The games take it as 80 by 24.")
	case t.Width < enoughCols || t.Height < enoughRows:
		row(windowTitle, size, fmt.Sprintf("Smaller than %d columns by %d rows. %s.", enoughCols, enoughRows, sentence(sharper)))
	default:
		row(windowTitle, size, enough)
	}
	w.Flush()
	return 0
}

// sentence returns s with its first letter a capital, for words the page has in mid-sentence.
func sentence(s string) string {
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}
