// Command atoll-agent is the Atoll worker-node daemon.
//
//	atoll-agent join --server URL --token JOIN [--config /etc/atoll/agent.json]
//	atoll-agent run  [--config /etc/atoll/agent.json]
//	atoll-agent version
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/atoll-sh/atoll/internal/agent"
	"github.com/atoll-sh/atoll/internal/agent/firewall"
	"github.com/atoll-sh/atoll/internal/agent/zeabur"
)

func usage() {
	fmt.Fprintf(os.Stderr, `atoll-agent %s

Usage:
  atoll-agent join --server URL --token JOIN_TOKEN   register this host with the control server
  atoll-agent run                                     run the daemon
  atoll-agent version

Flags for join/run:
  --config PATH   agent config (default %s)
`, agent.Version, agent.DefaultConfigPath)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	switch os.Args[1] {
	case "version", "--version", "-v":
		fmt.Println(agent.Version)
	case "join":
		fs := flag.NewFlagSet("join", flag.ExitOnError)
		server := fs.String("server", "", "control server URL, e.g. https://atoll.example.com")
		token := fs.String("token", "", "one-time join token")
		cfgPath := fs.String("config", agent.DefaultConfigPath, "config path")
		_ = fs.Parse(os.Args[2:])
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		var rt agent.Runtime
		if d, err := agent.NewDockerRuntime(); err == nil {
			rt = d
		}
		hello := agent.CollectHello(ctx, rt, "")
		cfg, err := agent.Join(ctx, *server, *token, hello, *cfgPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		fmt.Printf("joined %s as node %s; config written to %s\n", cfg.ServerURL, cfg.NodeID, *cfgPath)
	case "run":
		fs := flag.NewFlagSet("run", flag.ExitOnError)
		cfgPath := fs.String("config", agent.DefaultConfigPath, "config path")
		_ = fs.Parse(os.Args[2:])
		if err := run(*cfgPath, log); err != nil {
			log.Error("fatal", "err", err)
			os.Exit(1)
		}
	case "help", "-h", "--help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
}

func run(cfgPath string, log *slog.Logger) error {
	cfg, err := agent.LoadConfig(cfgPath)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Docker may start after us on boot; wait for it instead of failing.
	var rt *agent.DockerRuntime
	for {
		rt, err = agent.NewDockerRuntime()
		if err == nil {
			vctx, c := context.WithTimeout(ctx, 5*time.Second)
			_, err = rt.Version(vctx)
			c()
			if err == nil {
				break
			}
			rt.Close()
		}
		log.Warn("waiting for docker", "err", err)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(3 * time.Second):
		}
	}
	defer rt.Close()

	paths := agent.Paths{Root: cfg.DataDir}
	fw := firewall.New(cfg.FirewallEnabled(), paths.FirewallDir(), log)
	a := agent.New(cfg, rt, fw, log)

	// In-place Zeabur takeover support (no-op when k3s is absent).
	tk := zeabur.New(rt, paths, log)
	tk.Recover = a.Recover
	a.Ext = append(a.Ext, tk)
	a.Ingress.AddGuard(tk.IngressGuard)

	log.Info("atoll-agent starting", "version", agent.Version, "node", cfg.NodeID, "server", cfg.ServerURL,
		"data", filepath.Clean(cfg.DataDir), "firewall", cfg.FirewallEnabled())
	return a.Run(ctx)
}
