// Command atoll-agent is the Atoll worker-node daemon.
//
//	atoll-agent join --server URL --token JOIN [--config /etc/atoll/agent.json]
//	atoll-agent run  [--config /etc/atoll/agent.json]
//	atoll-agent tailnet status|up|switch
//	atoll-agent forward proto,listen,target ...   (runs inside mesh forwarder containers)
//	atoll-agent version
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/agent"
	"github.com/Sakuralaaa/Atoll/internal/agent/backup"
	"github.com/Sakuralaaa/Atoll/internal/agent/firewall"
	"github.com/Sakuralaaa/Atoll/internal/agent/mesh"
	"github.com/Sakuralaaa/Atoll/internal/agent/tailnet"
	"github.com/Sakuralaaa/Atoll/internal/agent/zeabur"
)

func usage() {
	fmt.Fprintf(os.Stderr, `atoll-agent %s

Usage:
  atoll-agent join --server URL --token JOIN_TOKEN   register this host with the control server
  atoll-agent run                                     run the daemon
  atoll-agent tailnet status                          Tailscale state and control URLs
  atoll-agent tailnet up [--authkey KEY] [--no-switch]
                                                      install Tailscale if needed, tailscale up (key from the
                                                      control server unless given), then switch the agent to the
                                                      control server's tailnet URL (verified first)
  atoll-agent tailnet switch --url URL                verify URL and make it the control URL (old one = fallback)
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
	case "forward":
		rules, err := mesh.ParseArgs(os.Args[2:])
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(2)
		}
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		if err := (&mesh.Forwarder{Log: log}).Run(ctx, rules); err != nil {
			log.Error("forward", "err", err)
			os.Exit(1)
		}
	case "tailnet":
		os.Exit(tailnetCmd(os.Args[2:]))
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
	a.CfgPath = cfgPath

	// Cross-server service mesh: forwarders over the tailnet, state kept locally.
	mk := mesh.New(rt, paths, log)
	mk.PruneNetwork = a.Mgr.PruneProjectNetwork
	a.Ext = append(a.Ext, mk)
	a.MeshErr = mk.Err
	go mk.Loop(ctx)

	// In-place Zeabur takeover support (no-op when k3s is absent).
	tk := zeabur.New(rt, paths, log)
	tk.Recover = a.Recover
	a.Ext = append(a.Ext, tk)
	a.Ingress.AddGuard(tk.IngressGuard)

	// Scheduled backups: restic in a container, straight to the S3 bucket; the schedule runs
	// locally so it does not depend on the control server.
	bk := backup.New(rt, paths, cfg.NodeID, a.Emit, log)
	bk.Locked = a.Mgr.Locked
	if cfg.BackupImage != "" {
		bk.Image = cfg.BackupImage
	}
	a.Ext = append(a.Ext, bk)
	go bk.Loop(ctx)

	log.Info("atoll-agent starting", "version", agent.Version, "node", cfg.NodeID, "server", cfg.ServerURL,
		"data", filepath.Clean(cfg.DataDir), "firewall", cfg.FirewallEnabled())
	return a.Run(ctx)
}

func tailnetCmd(args []string) int {
	if len(args) == 0 {
		usage()
		return 2
	}
	sub, args := args[0], args[1:]
	fs := flag.NewFlagSet("tailnet "+sub, flag.ExitOnError)
	cfgPath := fs.String("config", agent.DefaultConfigPath, "config path")
	authKey := fs.String("authkey", "", "Tailscale auth key (default: fetched from the control server)")
	hostname := fs.String("hostname", "", "tailscale --hostname (default atoll-<hostname>)")
	noSwitch := fs.Bool("no-switch", false, "only join the tailnet; keep the current control URL")
	url := fs.String("url", "", "control URL to switch to (tailnet switch)")
	noRestart := fs.Bool("no-restart", false, "do not restart atoll-agent after changing its config")
	_ = fs.Parse(args)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	tc := tailnet.New()
	switch sub {
	case "status":
		st := tc.Status(ctx)
		out := map[string]any{"installed": st.Installed, "state": st.State(), "tailnet_ip": st.IP, "dns_name": st.DNSName, "tailnet": st.Tailnet}
		if cfg, err := agent.LoadConfig(*cfgPath); err == nil {
			out["control_url"] = cfg.ServerURL
			out["fallback_urls"] = cfg.FallbackURLs
			out["via_tailnet"] = tailnet.IsTailnetURL(cfg.ServerURL)
		}
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b))
		return 0
	case "up":
		cfg, err := agent.LoadConfig(*cfgPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
		net, ferr := agent.FetchNetwork(ctx, cfg)
		key := *authKey
		if key == "" {
			if ferr != nil {
				fmt.Fprintln(os.Stderr, "error: could not fetch the network settings from the control server:", ferr)
				return 1
			}
			key = net.TailscaleAuthKey
		}
		if st := tc.Status(ctx); !st.Installed {
			fmt.Println("installing Tailscale…")
			if out, err := tc.Install(ctx); err != nil {
				fmt.Fprintln(os.Stderr, out)
				fmt.Fprintln(os.Stderr, "error:", err)
				return 1
			}
		}
		h := *hostname
		if h == "" {
			hn, _ := os.Hostname()
			h = tailnet.Hostname(hn)
		}
		st, err := tc.Up(ctx, key, h)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", tailnet.Redact(err.Error(), key))
			return 1
		}
		fmt.Printf("tailnet up: %s (%s)\n", st.IP, st.DNSName)
		if *noSwitch || ferr != nil || net.ControlURL == "" {
			if ferr == nil && net.ControlURL == "" {
				fmt.Println("the control server has no tailnet URL yet (Settings → Private network); control URL unchanged")
			}
			return 0
		}
		return switchURL(ctx, *cfgPath, net.ControlURL, net.PublicURL, *noRestart)
	case "switch":
		if *url == "" {
			fmt.Fprintln(os.Stderr, "error: --url is required")
			return 2
		}
		return switchURL(ctx, *cfgPath, *url, "", *noRestart)
	}
	usage()
	return 2
}

func switchURL(ctx context.Context, cfgPath, url, fallback string, noRestart bool) int {
	cfg, err := agent.SwitchConfigURL(ctx, cfgPath, url, fallback, nil, 90*time.Second)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	fmt.Printf("control URL is now %s (fallbacks: %v)\n", cfg.ServerURL, cfg.FallbackURLs)
	if noRestart {
		fmt.Println("restart atoll-agent to apply")
		return 0
	}
	if err := exec.CommandContext(ctx, "systemctl", "restart", "atoll-agent").Run(); err != nil {
		fmt.Println("restart atoll-agent to apply:", err)
	}
	return 0
}
