// Command atoll-server is the Atoll control plane: REST API, embedded web UI, agent hub.
//
//	atoll-server serve [--listen :7300] [--data /var/lib/atoll-server] [--public-url URL]
//	atoll-server reset-password [--data DIR] [--user NAME] [--password PW]
//	atoll-server admin reset-2fa [--data DIR] <user>
//	atoll-server version
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
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
  atoll-server version

Flags (serve):
  --listen ADDR      listen address (env ATOLL_LISTEN, default :7300)
  --data DIR         data directory (env ATOLL_DATA, default /var/lib/atoll-server)
  --public-url URL   URL agents and the CLI use (env ATOLL_PUBLIC_URL; Settings override it)
  --log-level LEVEL  debug|info|warn|error (env ATOLL_LOG_LEVEL, default info)
  --trusted-proxies  comma-separated IPs/CIDRs whose X-Forwarded-For is believed for the
                     client address (env ATOLL_TRUSTED_PROXIES, default loopback; "none")

Flags (reset-password):
  --data DIR  --user NAME (default: owner)  --password PW (default: generated)

Flags (admin reset-2fa):
  --data DIR
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

	srv, err := server.New(server.Config{DataDir: *data, PublicURL: *public, Logger: log, TrustedProxies: tp})
	if err != nil {
		log.Error("start", "err", err)
		return 1
	}
	hs := &http.Server{
		Addr:              *listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
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
