//go:build linux || darwin

package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestMain lets the test binary be tty1 itself: started again with TTY1_TEST_BE_TTY1 set, it runs
// main with the arguments it was given, so that a test can put the real program on a terminal.
func TestMain(m *testing.M) {
	if os.Getenv("TTY1_TEST_BE_TTY1") == "1" {
		main()
		return
	}
	os.Exit(m.Run())
}

// tty1 returns the real program, ready to be started with these arguments and this environment
// on top of the test's own.
func tty1(env []string, args ...string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(append(os.Environ(), "TTY1_TEST_BE_TTY1=1"), env...)
	return cmd
}

// collect reads everything a terminal is sent, at its other end, and returns a way to see what
// has arrived so far.
func collect(master *os.File) func() string {
	var mu sync.Mutex
	var seen bytes.Buffer
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			mu.Lock()
			seen.Write(buf[:n])
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		return seen.String()
	}
}

// waitFor waits until ok says yes. What a terminal is sent arrives when it arrives, so nothing
// here is read once and trusted to be whole.
func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("waited 10 s for %s", what)
}

// doctorOn runs `tty1 doctor` on a pseudo-terminal 100 by 30 whose other end sends answer once it
// has been asked, as a terminal would, and returns the exit status and all the terminal was sent.
func doctorOn(t *testing.T, answer string, env ...string) (int, string) {
	t.Helper()
	master, slave := openPty(t)
	if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Col: 100, Row: 30}); err != nil {
		t.Fatalf("giving the terminal its size: %v", err)
	}
	shown := collect(master)
	cmd := tty1(env, "doctor")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	// Answered only once asked: before the question the terminal is not yet in raw mode, and
	// would show the answer and hold it back for a line's end.
	waitFor(t, "the question", func() bool { return strings.Contains(shown(), "\x1b[c") })
	if _, err := master.WriteString(answer); err != nil {
		t.Fatalf("answering: %v", err)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		t.Fatalf("wanted doctor to finish within 10 s, and it had shown %q", shown())
	}
	waitFor(t, "the last line of the report", func() bool { return strings.Contains(shown(), windowTitle) })
	return cmd.ProcessState.ExitCode(), shown()
}

// The rows' titles, as internal/cli prints them.
const (
	keysTitle    = "Keys you can hold"
	coloursTitle = "All the colours"
	windowTitle  = "A big window"
)

// The whole of it on a terminal: the real program asks the question glonk's games ask, a terminal
// that speaks the kitty keyboard protocol answers, and the report says so.
func TestDoctorOnATerminalThatHasEverything(t *testing.T) {
	status, shown := doctorOn(t, "\x1b[?0u\x1b[?62c", "COLORTERM=truecolor")
	if status != 0 {
		t.Errorf("wanted status 0, got %d", status)
	}
	for _, want := range []string{keysTitle + "   yes", coloursTitle + "     yes", windowTitle + "        100 by 30"} {
		if !strings.Contains(shown, want) {
			t.Errorf("wanted %q on the terminal, got %q", want, shown)
		}
	}
	// It asks, and draws nothing: no other screen, no cursor moved, no colour set.
	if before, _, _ := strings.Cut(shown, keysTitle); before != "\x1b[?u\x1b[c" {
		t.Errorf("wanted only the two questions before the report, got %q", before)
	}
}

func TestDoctorOnAPlainTerminal(t *testing.T) {
	// Device attributes and nothing about keys, as a terminal without the protocol answers.
	status, shown := doctorOn(t, "\x1b[?62c", "COLORTERM=", "TERM=xterm-256color")
	if status != 0 {
		t.Errorf("wanted status 0, since everything still plays, got %d", status)
	}
	for _, want := range []string{keysTitle + "   no", coloursTitle + "     256", "export COLORTERM=truecolor", windowTitle + "        100 by 30"} {
		if !strings.Contains(shown, want) {
			t.Errorf("wanted %q on the terminal, got %q", want, shown)
		}
	}
}

// A terminal that answers nothing at all is given up on after a quarter second, and reported as
// one that holds a key at a time.
func TestDoctorOnATerminalThatDoesNotAnswer(t *testing.T) {
	status, shown := doctorOn(t, "", "COLORTERM=truecolor")
	if status != 0 || !strings.Contains(shown, keysTitle+"   no") {
		t.Errorf("wanted status 0 and no key releases, got %d and %q", status, shown)
	}
}

func TestDoctorWhereThereIsNoTerminal(t *testing.T) {
	_, slave := openPty(t)
	for what, wire := range map[string]func(*exec.Cmd){
		"the input is a pipe":         func(c *exec.Cmd) { c.Stdin, c.Stdout = strings.NewReader(""), slave },
		"the output is a pipe":        func(c *exec.Cmd) { c.Stdin, c.Stdout = slave, new(bytes.Buffer) },
		"neither is a terminal":       func(c *exec.Cmd) { c.Stdin, c.Stdout = strings.NewReader(""), new(bytes.Buffer) },
		"the input is closed, as <&-": func(c *exec.Cmd) { c.Stdout = new(bytes.Buffer) },
	} {
		cmd := tty1(nil, "doctor")
		wire(cmd)
		var errs bytes.Buffer
		cmd.Stderr = &errs
		err := cmd.Run()
		if cmd.ProcessState.ExitCode() != 1 || !strings.Contains(errs.String(), "has to run in a terminal") {
			t.Errorf("%s: wanted status 1 and the reason, got %v: %s", what, err, errs.String())
		}
	}
}
