// Package zeabur implements `atoll import zeabur`: it reads a Zeabur project through the
// Zeabur CLI that is already signed in on the user's machine (never asking for or storing
// Zeabur tokens or passwords), optionally reads the dedicated server's k3s state, builds an
// api.ImportPlan, exports images and data, applies the plan to Atoll and helps with cutover.
//
// It follows https://zeabur.com/blogs/migrate-from-zeabur.md: secret outputs are kept in
// memory or in chmod 600 files, nothing on Zeabur is changed except suspending application
// services during cutover after the user agrees, and a rollback path is always printed.
package zeabur

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Runner runs the Zeabur CLI. The default implementation shells out to
// `npx --yes zeabur@latest -i=false [--json] [--workspace ID] ...`.
type Runner interface {
	// Run returns stdout of a CLI call. json adds the global --json flag.
	Run(ctx context.Context, json bool, args ...string) ([]byte, error)
	// Stream runs a CLI call and copies stdout to w (used for exec streams).
	Stream(ctx context.Context, w io.Writer, args ...string) error
}

// CLI is the real Zeabur CLI runner.
type CLI struct {
	// Command is the program and leading args; default ["npx","--yes","zeabur@latest"].
	// Overridable with ATOLL_ZEABUR_CMD (space separated), e.g. "zeabur".
	Command   []string
	Workspace string
	Timeout   time.Duration // per call, default 60s
	// StreamTimeout bounds exec streams (dumps, tarballs, image exports), default 2h.
	StreamTimeout time.Duration
	Retries       int       // retries on 504 Gateway Timeout, default 3
	Stderr        io.Writer // where CLI stderr goes on failure (never contains our secrets)
	Verbose       bool
}

// NewCLI returns a runner with defaults.
func NewCLI(workspace string) *CLI {
	cmd := []string{"npx", "--yes", "zeabur@latest"}
	if v := strings.TrimSpace(os.Getenv("ATOLL_ZEABUR_CMD")); v != "" {
		cmd = strings.Fields(v)
	}
	return &CLI{Command: cmd, Workspace: workspace, Timeout: 60 * time.Second, StreamTimeout: 2 * time.Hour, Retries: 3, Stderr: os.Stderr}
}

func (c *CLI) argv(json bool, args []string) []string {
	out := append([]string{}, c.Command[1:]...)
	out = append(out, "-i=false")
	if json {
		out = append(out, "--json")
	}
	if c.Workspace != "" {
		out = append(out, "--workspace", c.Workspace)
	}
	return append(out, args...)
}

// ErrNotLoggedIn means the Zeabur CLI is not signed in on this machine.
var ErrNotLoggedIn = errors.New("the Zeabur CLI is not logged in (or returned 401). Run `npx zeabur@latest auth login` yourself in your terminal, then re-run this command; atoll never asks for Zeabur tokens or passwords")

func isGatewayTimeout(out []byte) bool {
	s := string(out)
	return strings.Contains(s, "504") || strings.Contains(s, "Gateway Timeout")
}

func isUnauthorized(out []byte) bool {
	s := string(out)
	return strings.Contains(s, "401") || strings.Contains(strings.ToLower(s), "unauthorized") || strings.Contains(strings.ToLower(s), "not logged in")
}

func (c *CLI) timeout() time.Duration {
	if c.Timeout <= 0 {
		return 60 * time.Second
	}
	return c.Timeout
}

// Run executes one CLI call with a timeout and retries on 504.
func (c *CLI) Run(ctx context.Context, json bool, args ...string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= c.Retries; attempt++ {
		cctx, cancel := context.WithTimeout(ctx, c.timeout())
		cmd := exec.CommandContext(cctx, c.Command[0], c.argv(json, args)...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		cancel()
		if err == nil {
			return stdout.Bytes(), nil
		}
		combined := append(stdout.Bytes(), stderr.Bytes()...)
		if isUnauthorized(combined) {
			return nil, ErrNotLoggedIn
		}
		lastErr = fmt.Errorf("zeabur %s: %v: %s", strings.Join(args, " "), err, lastLines(stderr.String()+stdout.String(), 3))
		if cctx.Err() == context.DeadlineExceeded {
			lastErr = fmt.Errorf("zeabur %s: timed out after %s", strings.Join(args, " "), c.timeout())
		}
		if !isGatewayTimeout(combined) && cctx.Err() == nil {
			return nil, lastErr
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		time.Sleep(time.Duration(attempt+1) * 2 * time.Second)
	}
	return nil, lastErr
}

// Stream executes a CLI call and copies stdout to w (no retry: a partial stream cannot
// be retried transparently; callers retry the whole step).
func (c *CLI) Stream(ctx context.Context, w io.Writer, args ...string) error {
	st := c.StreamTimeout
	if st <= 0 {
		st = 2 * time.Hour
	}
	cctx, cancel := context.WithTimeout(ctx, st)
	defer cancel()
	cmd := exec.CommandContext(cctx, c.Command[0], c.argv(false, args)...)
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = w, &stderr
	if err := cmd.Run(); err != nil {
		if isUnauthorized(stderr.Bytes()) {
			return ErrNotLoggedIn
		}
		if cctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("zeabur %s: timed out after %s", args[0], st)
		}
		return fmt.Errorf("zeabur %s: %v: %s", strings.Join(firstN(args, 3), " "), err, lastLines(stderr.String(), 3))
	}
	return nil
}

func firstN(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}

// ---------- remote command transports ----------

// Transport runs a shell command somewhere (on the dedicated server host, or inside a
// service container) and streams its stdout.
type Transport interface {
	// Output runs cmd (a /bin/sh script) and returns its stdout as text.
	Output(ctx context.Context, cmd string) (string, error)
	// Stream runs cmd and writes its raw binary stdout to w.
	Stream(ctx context.Context, cmd string, w io.Writer) error
	Describe() string
}

// shq single-quotes s for /bin/sh.
func shq(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// zeaburLogLine reports the CLI's own log lines that must be dropped from exec output.
func zeaburLogLine(line string) bool {
	return strings.HasPrefix(line, "INFO") || strings.HasPrefix(line, "ERROR") || strings.HasPrefix(line, "WARN")
}

// filterLines removes Zeabur CLI log lines from a text stream.
func filterLines(s string) string {
	var b strings.Builder
	for _, l := range strings.Split(s, "\n") {
		if zeaburLogLine(l) {
			continue
		}
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// base64Filter wraps w: what is written to it is the remote's `... | base64` output mixed with
// Zeabur CLI log lines; it drops those lines and decodes the rest into w.
type base64Filter struct {
	pw  *io.PipeWriter
	err chan error
}

func newBase64Filter(w io.Writer) *base64Filter {
	pr, pw := io.Pipe()
	f := &base64Filter{pw: pw, err: make(chan error, 1)}
	go func() {
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 16<<20)
		dpr, dpw := io.Pipe()
		done := make(chan error, 1)
		go func() {
			_, err := io.Copy(w, base64.NewDecoder(base64.StdEncoding, dpr))
			dpr.CloseWithError(err)
			done <- err
		}()
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || zeaburLogLine(line) {
				continue
			}
			if _, err := dpw.Write([]byte(line)); err != nil {
				break
			}
		}
		if err := sc.Err(); err != nil {
			dpw.CloseWithError(err)
			pr.CloseWithError(err)
		} else {
			dpw.Close()
		}
		derr := <-done
		if derr != nil {
			pr.CloseWithError(derr)
		}
		f.err <- derr
	}()
	return f
}

func (f *base64Filter) Write(p []byte) (int, error) { return f.pw.Write(p) }
func (f *base64Filter) Close() error {
	f.pw.Close()
	return <-f.err
}

// streamBase64 runs `cmd | base64` through run and decodes into w.
func streamBase64(ctx context.Context, cmd string, w io.Writer, run func(ctx context.Context, script string, w io.Writer) error) error {
	f := newBase64Filter(w)
	err := run(ctx, "set -o pipefail 2>/dev/null; ( "+cmd+" ) | base64", f)
	cerr := f.Close()
	if err != nil {
		return err
	}
	if cerr != nil {
		return fmt.Errorf("decode stream: %w", cerr)
	}
	return nil
}

// ServiceExec runs commands inside a service container with
// `zeabur service exec --id <serviceID> -- sh -c '...'` (works on shared clusters too).
type ServiceExec struct {
	CLI       Runner
	ServiceID string
}

func (s ServiceExec) Describe() string { return "zeabur service exec --id " + s.ServiceID }

func (s ServiceExec) raw(ctx context.Context, script string, w io.Writer) error {
	return s.CLI.Stream(ctx, w, "service", "exec", "--id", s.ServiceID, "--", "sh", "-c", script)
}

func (s ServiceExec) Output(ctx context.Context, cmd string) (string, error) {
	var buf bytes.Buffer
	if err := retry504(ctx, func() error { buf.Reset(); return s.raw(ctx, cmd, &buf) }); err != nil {
		return "", err
	}
	return filterLines(buf.String()), nil
}

func (s ServiceExec) Stream(ctx context.Context, cmd string, w io.Writer) error {
	return streamBase64(ctx, cmd, w, s.raw)
}

func retry504(ctx context.Context, fn func() error) error {
	var err error
	for i := 0; i < 4; i++ {
		if err = fn(); err == nil || !strings.Contains(err.Error(), "504") {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(i+1) * 2 * time.Second):
		}
	}
	return err
}

// HostAccess says how to reach the dedicated server host. It holds no secrets: SSH uses the
// user's own key file/agent, and server exec uses the credential Zeabur holds.
type HostAccess struct {
	Mode     string `json:"mode"` // "server-exec" | "ssh"
	ServerID string `json:"server_id,omitempty"`
	SSHDest  string `json:"ssh_dest,omitempty"` // user@host
	SSHPort  int    `json:"ssh_port,omitempty"`
	SSHKey   string `json:"ssh_key,omitempty"` // path to private key on this machine
	Sudo     bool   `json:"sudo,omitempty"`
}

// HostTransport runs commands on the dedicated server host.
type HostTransport struct {
	Access  HostAccess
	CLI     Runner
	Timeout time.Duration
}

func (h HostTransport) Describe() string {
	if h.Access.Mode == "ssh" {
		return "ssh " + h.Access.SSHDest
	}
	return "zeabur server exec " + h.Access.ServerID
}

func (h HostTransport) wrap(script string) string {
	if h.Access.Sudo {
		return "sudo sh -c " + shq(script)
	}
	return script
}

func (h HostTransport) raw(ctx context.Context, script string, w io.Writer) error {
	script = h.wrap(script)
	if h.Access.Mode == "ssh" {
		args := []string{"-o", "BatchMode=yes"}
		if h.Access.SSHPort > 0 {
			args = append(args, "-p", fmt.Sprint(h.Access.SSHPort))
		}
		if h.Access.SSHKey != "" {
			args = append(args, "-i", h.Access.SSHKey)
		}
		args = append(args, h.Access.SSHDest, "sh -c "+shq(script))
		t := h.Timeout
		if t <= 0 {
			t = 30 * time.Minute
		}
		cctx, cancel := context.WithTimeout(ctx, t)
		defer cancel()
		cmd := exec.CommandContext(cctx, "ssh", args...)
		var stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = w, &stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("ssh %s: %v: %s", h.Access.SSHDest, err, lastLines(stderr.String(), 3))
		}
		return nil
	}
	// Server ID is positional; --id does not work with server exec.
	return h.CLI.Stream(ctx, w, "server", "exec", h.Access.ServerID, "--", "sh", "-c", script)
}

func (h HostTransport) Output(ctx context.Context, cmd string) (string, error) {
	var buf bytes.Buffer
	if err := retry504(ctx, func() error { buf.Reset(); return h.raw(ctx, cmd, &buf) }); err != nil {
		return "", err
	}
	if h.Access.Mode == "ssh" {
		return strings.TrimRight(buf.String(), "\n"), nil
	}
	return filterLines(buf.String()), nil
}

func (h HostTransport) Stream(ctx context.Context, cmd string, w io.Writer) error {
	if h.Access.Mode == "ssh" {
		return h.raw(ctx, cmd, w)
	}
	return streamBase64(ctx, cmd, w, h.raw)
}

// PodExec runs commands inside a service's pod through the host's kubectl.
type PodExec struct {
	Host      HostTransport
	Namespace string
	Workload  string // e.g. deploy/service-<id> or sts/service-<id>
}

func (p PodExec) Describe() string { return p.Host.Describe() + " → kubectl exec " + p.Workload }

func (p PodExec) script(cmd string) string {
	return "k3s kubectl -n " + shq(p.Namespace) + " exec " + shq(p.Workload) + " -- sh -c " + shq(cmd)
}

func (p PodExec) Output(ctx context.Context, cmd string) (string, error) {
	return p.Host.Output(ctx, p.script(cmd))
}

func (p PodExec) Stream(ctx context.Context, cmd string, w io.Writer) error {
	return p.Host.Stream(ctx, p.script(cmd), w)
}
