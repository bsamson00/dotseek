package dotseek

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"
)

func configureColors(enabled bool) {
	if enabled {
		colorGreen = "\033[92m"
		colorRed = "\033[91m"
		colorYellow = "\033[93m"
		colorTLD = "\033[90m"
		colorReset = "\033[0m"
		return
	}
	colorGreen = ""
	colorRed = ""
	colorYellow = ""
	colorTLD = ""
	colorReset = ""
}

func isTerminalFile(file *os.File) bool {
	if file == nil {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func isTerminalWriter(w io.Writer) bool {
	file, ok := w.(*os.File)
	return ok && isTerminalFile(file)
}

func colorEnabled(mode string, w io.Writer) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "auto":
		return isTerminalWriter(w) && os.Getenv("NO_COLOR") == "", nil
	case "always":
		return true, nil
	case "never":
		return false, nil
	default:
		return false, fmt.Errorf("invalid --color value %q; use auto, always, or never", mode)
	}
}

func outputTerminalWidth(w io.Writer) int {
	if file, ok := w.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		if width, _, err := term.GetSize(int(file.Fd())); err == nil && width > 0 {
			return width
		}
	}
	if width, err := strconv.Atoi(strings.TrimSpace(os.Getenv("COLUMNS"))); err == nil && width > 0 {
		return width
	}
	return 100
}

func gridColumnCount(terminalWidth, cellWidth int) int {
	if terminalWidth <= 0 {
		terminalWidth = 100
	}
	if cellWidth <= 0 {
		cellWidth = 1
	}
	columns := terminalWidth / cellWidth
	if columns < 1 {
		return 1
	}
	if columns > 5 {
		return 5
	}
	return columns
}

func sanitizeTerminalText(value string) string {
	return strings.Map(func(r rune) rune {
		if r < ' ' || r == 0x7f {
			return ' '
		}
		return r
	}, value)
}

// withSpinner runs fn while showing an animated spinner (on stderr) with the
// given label, then clears the line and returns fn's result. When stderr is not
// a terminal the spinner is skipped and fn runs directly. Pressing 'q' to quit is
// handled globally by the quit listener (see startQuitListener).
func withSpinner(label string, fn func() string) string {
	if !isTerminalWriter(commandStderr) {
		return fn()
	}
	done := make(chan string, 1)
	go func() { done <- fn() }()

	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	for i := 0; ; i++ {
		select {
		case res := <-done:
			fmt.Fprint(commandStderr, "\r\033[K")
			return res
		case <-time.After(80 * time.Millisecond):
			fmt.Fprintf(commandStderr, "\r%s %s %s(press q to quit)%s", frames[i%len(frames)], label, colorYellow, colorReset)
		}
	}
}

// restoreTerminal reverts any terminal mode change made by startQuitListener.
// It is a no-op until the listener installs cbreak mode.
var restoreTerminal = func() {}

// stty runs the stty utility against the controlling terminal (os.Stdin). It is
// POSIX and works on both macOS and Linux, avoiding OS-specific termios code.
func stty(args ...string) ([]byte, error) {
	cmd := exec.Command("stty", args...)
	cmd.Stdin = commandStdin
	return cmd.Output()
}

// startQuitListener puts the terminal in cbreak mode (line buffering and echo
// off, but output processing left intact so normal \n output is unaffected) and
// watches stdin for 'q'/'Q' to quit. It is a no-op when stdin is not a terminal.
// Callers must ensure restoreTerminal() runs on every exit path.
func startQuitListener(ctx context.Context, cancel context.CancelFunc) {
	fi, err := commandStdin.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return
	}

	saved, err := stty("-g")
	if err != nil {
		return
	}
	savedState := strings.TrimSpace(string(saved))

	if _, err := stty("cbreak", "-echo"); err != nil {
		return
	}
	restoreTerminal = func() { stty(savedState) }

	go func() {
		buf := make([]byte, 1)
		for {
			if ctx.Err() != nil {
				return
			}

			_ = commandStdin.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
			n, err := commandStdin.Read(buf)
			if err != nil {
				if os.IsTimeout(err) {
					continue
				}
				return
			}
			if n > 0 && (buf[0] == 'q' || buf[0] == 'Q') {
				restoreTerminal()
				fmt.Fprintf(commandStderr, "\r\033[K\n%sQuit.%s\n", colorYellow, colorReset)
				flushCache()
				cancel()
				return
			}
		}
	}()
}
