// Package zeabur implements the agent side of the in-place Zeabur takeover: on a VPS that
// Zeabur manages with single-node k3s, it inventories Zeabur's objects, moves images, PVC
// data and databases into the host's Docker, scales Zeabur workloads, parks/restores its
// ingress controller and finally (on explicit request) uninstalls k3s.
//
// All host commands go through Exec so tests can replay fixtures. Zeabur objects are never
// deleted; every change (replicas, nodeSelector, node label) is recorded in
// /var/lib/atoll/zeabur-takeover.json for rollback.
package zeabur

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Exec runs host commands.
type Exec interface {
	// Output runs the command and returns stdout; a non-zero exit returns an error with stderr.
	Output(ctx context.Context, name string, args ...string) ([]byte, error)
	// Stream runs the command with stdin (may be nil) and streams stdout to w.
	Stream(ctx context.Context, stdin io.Reader, w io.Writer, name string, args ...string) error
	// LookPath reports whether a binary exists.
	LookPath(name string) (string, error)
}

// HostExec is the real Exec.
type HostExec struct{}

func (HostExec) LookPath(name string) (string, error) {
	if strings.HasPrefix(name, "/") {
		if _, err := os.Stat(name); err != nil {
			return "", err
		}
		return name, nil
	}
	return exec.LookPath(name)
}

func (HostExec) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("%s %s: %v: %s", name, strings.Join(redact(args), " "), err, lastN(stderr.String(), 2000))
	}
	return out, nil
}

func (HostExec) Stream(ctx context.Context, stdin io.Reader, w io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = stdin
	cmd.Stdout = w
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %v: %s", name, strings.Join(redact(args), " "), err, lastN(stderr.String(), 2000))
	}
	return nil
}

// redact hides `sh -c` scripts (they can carry credentials via env expansion) in errors.
func redact(args []string) []string {
	out := append([]string(nil), args...)
	for i := range out {
		if i > 0 && out[i-1] == "-c" && len(out[i]) > 60 {
			out[i] = out[i][:60] + "…"
		}
	}
	return out
}

func lastN(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return "…" + s[len(s)-n:]
	}
	return s
}

func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
