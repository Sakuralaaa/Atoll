package backup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Runner runs host commands (the docker CLI). env holds KEY=VALUE pairs added to the
// command's environment: secrets travel only there, never in argv.
type Runner interface {
	Run(ctx context.Context, env []string, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error
}

// ExitError is a command that ran and exited non-zero.
type ExitError struct {
	Code   int
	Stderr string
}

func (e *ExitError) Error() string {
	msg := lastLines(e.Stderr, 6, 1500)
	if msg == "" {
		return fmt.Sprintf("exit status %d", e.Code)
	}
	return fmt.Sprintf("exit status %d: %s", e.Code, msg)
}

// exitCode returns the exit code of err (-1 when it is not an ExitError).
func exitCode(err error) int {
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	return -1
}

// HostRunner is the real Runner.
type HostRunner struct{}

func (HostRunner) Run(ctx context.Context, env []string, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	var eb bytes.Buffer
	if stderr != nil {
		cmd.Stderr = io.MultiWriter(stderr, &tailBuffer{b: &eb, max: 16 << 10})
	} else {
		cmd.Stderr = &tailBuffer{b: &eb, max: 16 << 10}
	}
	err := cmd.Run()
	var xe *exec.ExitError
	if errors.As(err, &xe) {
		return &ExitError{Code: xe.ExitCode(), Stderr: eb.String()}
	}
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// tailBuffer keeps roughly the last max bytes written.
type tailBuffer struct {
	b   *bytes.Buffer
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.b.Write(p)
	if over := t.b.Len() - t.max; over > 0 {
		rest := append([]byte(nil), t.b.Bytes()[over:]...)
		t.b.Reset()
		t.b.Write(rest)
	}
	return len(p), nil
}

func lastLines(s string, n, max int) string {
	s = strings.TrimSpace(s)
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	s = strings.Join(lines, " | ")
	if len(s) > max {
		s = "…" + s[len(s)-max:]
	}
	return s
}
