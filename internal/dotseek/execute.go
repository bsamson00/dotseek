package dotseek

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

type cliError struct {
	code int
	err  error
	hint string
}

func (e *cliError) Error() string { return e.err.Error() }

func (e *cliError) Unwrap() error { return e.err }

const (
	exitGeneral     = 1
	exitUsage       = 2
	exitConfig      = 3
	exitAPI         = 4
	exitPartial     = 5
	exitIO          = 6
	exitInterrupted = 130
)

func exitCode(err error) int {
	var cliErr *cliError
	if errors.As(err, &cliErr) {
		return cliErr.code
	}
	return exitGeneral
}

// Execute runs DotSeek with the supplied process arguments and streams,
// returning the stable process exit code that the launcher should use.
func Execute(args []string, stdin *os.File, stdout, stderr io.Writer) int {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	deps := defaultRuntimeDependencies()
	deps.stdin = stdin
	deps.stdout = stdout
	deps.stderr = stderr
	if err := run(ctx, cancel, args, deps); err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		var cliErr *cliError
		if errors.As(err, &cliErr) && cliErr.hint != "" {
			fmt.Fprintf(stderr, "Next: %s\n", cliErr.hint)
		}
		return exitCode(err)
	}
	return 0
}
