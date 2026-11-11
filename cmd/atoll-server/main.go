// Command atoll-server is the Atoll control plane: REST API, embedded web UI, agent hub.
//
//	atoll-server serve [--listen :7300] [--data /var/lib/atoll-server] [--public-url URL]
//	atoll-server reset-password [--data DIR] [--user NAME] [--password PW]
//	atoll-server admin reset-2fa [--data DIR] <user>
//	atoll-server export --out FILE --passphrase-file F [--data DIR]
//	atoll-server restore --bundle FILE --passphrase-file F [--data DIR] [--force] [--adopt-public-ip]
//	atoll-server version
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/server"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func usage() {
	fmt.Fprintf(os.Stderr, `atoll-server %s

Usage:
  atoll-server serve            run the control server (default command)
  atoll-server reset-password   set a new panel password (prints it)
  atoll-server admin reset-2fa <user>
                                turn off two-factor login for a user (lost phone);
                                signs the user out everywhere
  atoll-server export           write an encrypted control bundle (offline; see docs/control.md)
  atoll-server restore          install a control bundle into the data dir (stop the server first)
  atoll-server version

Flags (serve):
  --listen ADDR      listen address (env ATOLL_LISTEN, default :7300)
  --data DIR         data directory (env ATOLL_DATA, default /var/lib/atoll-server)
  --public-url URL   URL agents and the CLI use (env ATOLL_PUBLIC_URL; Settings override it)
  --log-level LEVEL  debug|info|warn|error (env ATOLL_LOG_LEVEL, default info)
  --trusted-proxies  comma-separated IPs/CIDRs whose X-Forwarded-For is believed for the
                     client address (env ATOLL_TRUSTED_PROXIES, default loopback; "none")
  --adopt-public-ip  point the panel's DNS record (public URL host inside the Cloudflare zone)
                     at this host's public IPv4 on start (env ATOLL_ADOPT_PUBLIC_IP=1;
                     ATOLL_PUBLIC_IP overrides detection). Used after moving the control server.

Deployment, env only:
  ATOLL_DEPLOYMENT=docker             running from the container image (also guessed from /.dockerenv):
                                      self-update is off; update with docker compose pull && up -d

Private network (Tailscale), env only:
  ATOLL_AGENT_LISTEN=tailscale:7301   extra listener for agent endpoints on this host's tailnet IP
  ATOLL_AGENT_TAILNET_ONLY=1          stop answering /agent/* publicly (bootstrap stays public)
  ATOLL_MESH_PORTS=41000-41999        ports published on tailnet IPs for cross-server services

Flags (reset-password):
  --data DIR  --user NAME (default: owner)  --password PW (default: generated)

Flags (admin reset-2fa):
  --data DIR

Flags (export):
  --out FILE  --passphrase-file F (min 12 characters; "-" = stdin)  --data DIR
  --env-file PATH (default /etc/atoll/server.env)  --caddyfile PATH (default /etc/caddy/Caddyfile)

Flags (restore):
  --bundle FILE  --passphrase-file F ("-" = stdin)  --data DIR
  --env-file PATH   merge the bundle's server.env into PATH (existing keys win; "" = skip)
  --force           replace an existing database (kept in DATA/pre-restore-<time>/)
  --adopt-public-ip on the next start, point the panel's DNS record at this host
  --keep-tailnet-url  keep a stored tailnet control URL that is an IP address
`, server.Version)
}

func main() {
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "version", "--version":
		fmt.Println(server.Version)
	case "serve":
		os.Exit(serve(args))
	case "reset-password":
		os.Exit(resetPassword(args))
	case "admin":
		os.Exit(admin(args))
	case "export":
		os.Exit(exportCmd(args))
	case "restore":
		os.Exit(restoreCmd(args))
	case "control-helper":
		os.Exit(controlHelper(args))
	case "help", "-h", "--help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
}

func serve(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	fs.Usage = usage
	listen := fs.String("listen", env("ATOLL_LISTEN", ":7300"), "listen address")
	data := fs.String("data", env("ATOLL_DATA", "/var/lib/atoll-server"), "data directory")
	public := fs.String("public-url", env("ATOLL_PUBLIC_URL", ""), "public URL")
	level := fs.String("log-level", env("ATOLL_LOG_LEVEL", "info"), "log level")
	proxies := fs.String("trusted-proxies", env("ATOLL_TRUSTED_PROXIES", ""), "trusted proxies")
	adopt := fs.Bool("adopt-public-ip", truthy(env("ATOLL_ADOPT_PUBLIC_IP", "")), "update the panel DNS record to this host")
	fs.Parse(args)
	tp, err := server.ParseTrustedProxies(*proxies)
	if err != nil {
		fmt.Fprintln(os.Stderr, "--trusted-proxies:", err)
		return 2
	}

	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(*level)); err != nil {
		lvl = slog.LevelInfo
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
	slog.SetDefault(log)

	srv, err := server.New(server.Config{DataDir: *data, PublicURL: *public, Logger: log, TrustedProxies: tp,
		ListenAddr: *listen, ServerEnvPath: env("ATOLL_SERVER_ENV", server.DefaultServerEnvPath),
		CaddyfilePath: env("ATOLL_CADDYFILE", server.DefaultCaddyfilePath), UnitName: env("ATOLL_SYSTEMD_UNIT", "atoll-server"),
		AdoptPublicIP: *adopt, PublicIP: env("ATOLL_PUBLIC_IP", ""), Container: inContainer()})
	if err != nil {
		log.Error("start", "err", err)
		return 1
	}
	lo, hi, err := server.ParseMeshPorts(env("ATOLL_MESH_PORTS", ""))
	if err != nil {
		log.Error("start", "err", err)
		return 1
	}
	tailnetOnly := false
	switch env("ATOLL_AGENT_TAILNET_ONLY", "") {
	case "1", "true", "yes", "on":
		tailnetOnly = true
	}
	srv.SetNetworkOptions(server.NetOptions{AgentListen: env("ATOLL_AGENT_LISTEN", ""), AgentTailnetOnly: tailnetOnly, MeshPortLo: lo, MeshPortHi: hi})
	hs := &http.Server{
		Addr:              *listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go srv.ServeAgentListener(ctx) // no-op unless ATOLL_AGENT_LISTEN is set
	errc := make(chan error, 1)
	go func() {
		log.Info("atoll-server listening", "addr", *listen, "data", *data, "version", server.Version)
		errc <- hs.ListenAndServe()
	}()
	code := 0
	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Error("listen", "err", err)
			code = 1
		}
	case <-ctx.Done():
		log.Info("shutting down")
	}
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	hs.Shutdown(sctx)
	srv.Close()
	return code
}

func resetPassword(args []string) int {
	fs := flag.NewFlagSet("reset-password", flag.ExitOnError)
	fs.Usage = usage
	data := fs.String("data", env("ATOLL_DATA", "/var/lib/atoll-server"), "data directory")
	user := fs.String("user", "", "username (default: owner)")
	pw := fs.String("password", "", "new password (default: generated)")
	fs.Parse(args)
	name, password, err := server.ResetPassword(*data, *user, *pw)
	if err != nil {
		fmt.Fprintln(os.Stderr, "reset-password:", err)
		return 1
	}
	fmt.Printf("password for %s reset; all sessions signed out\nnew password: %s\n", name, password)
	return 0
}

func admin(args []string) int {
	if len(args) == 0 || args[0] != "reset-2fa" {
		fmt.Fprintln(os.Stderr, "usage: atoll-server admin reset-2fa [--data DIR] <user>")
		return 2
	}
	fs := flag.NewFlagSet("admin reset-2fa", flag.ExitOnError)
	fs.Usage = usage
	data := fs.String("data", env("ATOLL_DATA", "/var/lib/atoll-server"), "data directory")
	// accept flags before or after the user name
	fs.Parse(args[1:])
	rest := fs.Args()
	user := ""
	if len(rest) > 0 {
		user, rest = rest[0], rest[1:]
		fs.Parse(rest)
	}
	if user == "" || len(fs.Args()) > 0 {
		fmt.Fprintln(os.Stderr, "usage: atoll-server admin reset-2fa [--data DIR] <user>")
		return 2
	}
	name, err := server.Reset2FA(*data, user)
	if err != nil {
		fmt.Fprintln(os.Stderr, "reset-2fa:", err)
		return 1
	}
	fmt.Printf("two-factor authentication for %s turned off; all sessions signed out\nlog in with the password and enable it again under Settings -> Security\n", name)
	return 0
}

// inContainer: ATOLL_DEPLOYMENT=docker (set by the official image), or a Docker container marker.
// ATOLL_DEPLOYMENT=systemd (or any other value) turns the /.dockerenv guess off.
func inContainer() bool {
	if d := strings.ToLower(strings.TrimSpace(os.Getenv("ATOLL_DEPLOYMENT"))); d != "" {
		return d == "docker" || d == "container"
	}
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// readPassphrase reads the first line of path ("-" = stdin).
func readPassphrase(path string) (string, error) {
	if path == "" {
		return "", errors.New("--passphrase-file is required")
	}
	var b []byte
	var err error
	if path == "-" {
		b, err = io.ReadAll(io.LimitReader(os.Stdin, 4096))
	} else {
		b, err = os.ReadFile(path)
	}
	if err != nil {
		return "", err
	}
	line, _, _ := strings.Cut(string(b), "\n")
	return strings.TrimRight(line, "\r"), nil
}

func exportCmd(args []string) int {
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	fs.Usage = usage
	data := fs.String("data", env("ATOLL_DATA", "/var/lib/atoll-server"), "data directory")
	out := fs.String("out", "", "output file")
	pf := fs.String("passphrase-file", "", "file holding the passphrase")
	envFile := fs.String("env-file", env("ATOLL_SERVER_ENV", server.DefaultServerEnvPath), "server.env to include")
	caddy := fs.String("caddyfile", env("ATOLL_CADDYFILE", server.DefaultCaddyfilePath), "Caddyfile to include")
	fs.Parse(args)
	if *out == "" {
		fmt.Fprintln(os.Stderr, "export: --out FILE is required")
		return 2
	}
	pass, err := readPassphrase(*pf)
	if err != nil {
		fmt.Fprintln(os.Stderr, "export:", err)
		return 2
	}
	m, err := server.ExportControl(context.Background(), *data, *out, pass, *envFile, *caddy)
	if err != nil {
		fmt.Fprintln(os.Stderr, "export:", err)
		return 1
	}
	fmt.Printf("wrote %s (%s, files: %s)\nkeep the passphrase: the bundle cannot be opened without it\n", *out, m.Version, strings.Join(m.Files, ", "))
	return 0
}

func restoreCmd(args []string) int {
	fs := flag.NewFlagSet("restore", flag.ExitOnError)
	fs.Usage = usage
	data := fs.String("data", env("ATOLL_DATA", "/var/lib/atoll-server"), "data directory")
	bundle := fs.String("bundle", "", "bundle file")
	pf := fs.String("passphrase-file", "", "file holding the passphrase")
	envFile := fs.String("env-file", "", "merge the bundle's server.env into this file")
	force := fs.Bool("force", false, "replace an existing database")
	adopt := fs.Bool("adopt-public-ip", false, "point the panel DNS record at this host on the next start")
	keepTS := fs.Bool("keep-tailnet-url", false, "keep a stored tailnet control URL that is an IP")
	fs.Parse(args)
	if *bundle == "" {
		fmt.Fprintln(os.Stderr, "restore: --bundle FILE is required")
		return 2
	}
	pass, err := readPassphrase(*pf)
	if err != nil {
		fmt.Fprintln(os.Stderr, "restore:", err)
		return 2
	}
	res, err := server.RestoreControl(context.Background(), server.RestoreOptions{Bundle: *bundle, Passphrase: pass, DataDir: *data,
		EnvFile: *envFile, Force: *force, AdoptPublicIP: *adopt, KeepTailnetURL: *keepTS})
	if err != nil {
		fmt.Fprintln(os.Stderr, "restore:", err)
		return 1
	}
	m := res.Manifest
	fmt.Printf("restored control state from %s (atoll-server %s on %s, %s) into %s; %d servers\n", *bundle, m.Version, m.Hostname, m.CreatedAt.Format(time.RFC3339), *data, res.Nodes)
	if res.MovedAside != "" {
		fmt.Printf("the previous database and key were moved to %s\n", res.MovedAside)
	}
	if res.EnvWritten {
		fmt.Printf("merged server.env into %s\n", *envFile)
	}
	if res.CaddyfileSaved != "" {
		fmt.Printf("the old Caddyfile is at %s (for reference; not installed)\n", res.CaddyfileSaved)
	}
	if res.TailnetCleared != "" {
		fmt.Printf("cleared the stored tailnet control URL %s (this host announces its own)\n", res.TailnetCleared)
	}
	if *adopt {
		fmt.Println("on its next start atoll-server points the panel's DNS record at this host")
	}
	fmt.Println("start it with: systemctl start atoll-server")
	return 0
}

// controlHelper is the detached self-update helper started by systemd-run (control_update.go).
func controlHelper(args []string) int {
	fs := flag.NewFlagSet("control-helper", flag.ExitOnError)
	var o server.HelperOptions
	fs.StringVar(&o.Mode, "mode", "update", "update | rollback")
	fs.StringVar(&o.Target, "target", "/usr/local/bin/atoll-server", "installed binary")
	fs.StringVar(&o.New, "new", "", "verified new binary (update)")
	fs.StringVar(&o.Unit, "unit", "atoll-server", "systemd unit")
	fs.StringVar(&o.Health, "health", "http://127.0.0.1:7300", "local base URL")
	fs.StringVar(&o.Result, "result", "", "result file")
	fs.DurationVar(&o.Timeout, "timeout", 60*time.Second, "health timeout")
	fs.Parse(args)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	res := server.RunHelper(ctx, o)
	if !res.OK {
		fmt.Fprintln(os.Stderr, "control-helper:", res.Error)
		return 1
	}
	fmt.Printf("control-helper: %s %s -> %s ok\n", res.Mode, res.FromVersion, res.ToVersion)
	return 0
}
