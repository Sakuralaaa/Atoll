package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/atoll-sh/atoll/internal/api"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	gnet "github.com/shirou/gopsutil/v4/net"
)

// DiskPath is the filesystem measured for disk metrics (where /var/lib/atoll lives).
var DiskPath = "/"

// CollectHello gathers host facts for join and the WS hello frame. Network lookups are
// best-effort and bounded by ctx.
func CollectHello(ctx context.Context, rt Runtime, nodeID string) api.HelloPayload {
	h := api.HelloPayload{NodeID: nodeID, AgentVersion: Version, OS: runtime.GOOS, Arch: runtime.GOARCH}
	h.Hostname, _ = os.Hostname()
	if info, err := host.InfoWithContext(ctx); err == nil {
		h.OS = strings.TrimSpace(info.Platform + " " + info.PlatformVersion)
		if h.OS == "" {
			h.OS = info.OS
		}
	}
	if n, err := cpu.CountsWithContext(ctx, true); err == nil {
		h.CPUCores = n
	}
	if vm, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		h.MemMB = int(vm.Total / (1024 * 1024))
	}
	if du, err := disk.UsageWithContext(ctx, DiskPath); err == nil {
		h.DiskGB = int(du.Total / (1024 * 1024 * 1024))
	}
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); h.PublicIPv4 = publicIP(ctx, "https://api.ipify.org", false) }()
	go func() { defer wg.Done(); h.PublicIPv6 = publicIP(ctx, "https://api6.ipify.org", true) }()
	go func() { defer wg.Done(); h.TailnetHost = tailnetHost(ctx) }()
	if rt != nil {
		if v, err := rt.Version(ctx); err == nil {
			h.DockerVersion = v
		}
	}
	wg.Wait()
	return h
}

func publicIP(ctx context.Context, url string, v6 bool) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 128))
	ip := net.ParseIP(strings.TrimSpace(string(b)))
	if ip == nil || (ip.To4() == nil) != v6 {
		return ""
	}
	return ip.String()
}

// tailnetHost returns this node's MagicDNS name (or first Tailscale IP) if tailscale runs.
func tailnetHost(ctx context.Context) string {
	path, err := exec.LookPath("tailscale")
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "status", "--json").Output()
	if err != nil {
		return ""
	}
	return parseTailscaleStatus(out)
}

func parseTailscaleStatus(b []byte) string {
	var st struct {
		Self struct {
			DNSName      string   `json:"DNSName"`
			TailscaleIPs []string `json:"TailscaleIPs"`
		} `json:"Self"`
	}
	if err := json.NewDecoder(bytes.NewReader(b)).Decode(&st); err != nil {
		return ""
	}
	if n := strings.TrimSuffix(st.Self.DNSName, "."); n != "" {
		return n
	}
	if len(st.Self.TailscaleIPs) > 0 {
		return st.Self.TailscaleIPs[0]
	}
	return ""
}

// NodeSampler computes node metrics; network rates are deltas between calls.
type NodeSampler struct {
	lastAt     time.Time
	lastRx     uint64
	lastTx     uint64
	containers map[string]netSample
}

type netSample struct {
	at     time.Time
	rx, tx uint64
}

func (s *NodeSampler) Node(ctx context.Context) api.NodeMetrics {
	now := time.Now().UTC()
	m := api.NodeMetrics{At: now}
	if p, err := cpu.PercentWithContext(ctx, 0, false); err == nil && len(p) > 0 {
		m.CPUPct = p[0]
	}
	if vm, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		m.MemUsedMB = int((vm.Total - vm.Available) / (1024 * 1024))
		m.MemTotalMB = int(vm.Total / (1024 * 1024))
	}
	if du, err := disk.UsageWithContext(ctx, DiskPath); err == nil {
		m.DiskUsedGB = float64(du.Used) / (1 << 30)
		m.DiskTotalGB = float64(du.Total) / (1 << 30)
	}
	if l, err := load.AvgWithContext(ctx); err == nil {
		m.Load1 = l.Load1
	}
	if io, err := gnet.IOCountersWithContext(ctx, true); err == nil {
		var rx, tx uint64
		for _, c := range io {
			// physical-ish interfaces only: skip loopback and docker/veth/bridge traffic
			if c.Name == "lo" || strings.HasPrefix(c.Name, "veth") || strings.HasPrefix(c.Name, "docker") ||
				strings.HasPrefix(c.Name, "br-") || strings.HasPrefix(c.Name, "cni") || strings.HasPrefix(c.Name, "flannel") {
				continue
			}
			rx += c.BytesRecv
			tx += c.BytesSent
		}
		if !s.lastAt.IsZero() && rx >= s.lastRx && tx >= s.lastTx {
			dt := now.Sub(s.lastAt).Seconds()
			if dt > 0 {
				m.NetRxBps = float64(rx-s.lastRx) / dt
				m.NetTxBps = float64(tx-s.lastTx) / dt
			}
		}
		s.lastAt, s.lastRx, s.lastTx = now, rx, tx
	}
	return m
}

// Containers samples every running atoll service container.
func (s *NodeSampler) Containers(ctx context.Context, rt Runtime) []api.ContainerMetrics {
	if s.containers == nil {
		s.containers = map[string]netSample{}
	}
	list, err := rt.ListContainers(ctx, LabelService)
	if err != nil {
		return nil
	}
	var out []api.ContainerMetrics
	for _, c := range list {
		sid := c.Labels[LabelService]
		if c.State != "running" || c.Name != ContainerName(sid) {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		st, err := rt.Stats(cctx, c.Name)
		cancel()
		if err != nil {
			continue
		}
		now := time.Now().UTC()
		cm := api.ContainerMetrics{ServiceID: sid, At: now, CPUPct: st.CPUPct, MemMB: st.MemMB}
		if prev, ok := s.containers[sid]; ok && st.RxBytes >= prev.rx && st.TxBytes >= prev.tx {
			if dt := now.Sub(prev.at).Seconds(); dt > 0 {
				cm.NetRxBps = float64(st.RxBytes-prev.rx) / dt
				cm.NetTxBps = float64(st.TxBytes-prev.tx) / dt
			}
		}
		s.containers[sid] = netSample{at: now, rx: st.RxBytes, tx: st.TxBytes}
		out = append(out, cm)
	}
	return out
}
