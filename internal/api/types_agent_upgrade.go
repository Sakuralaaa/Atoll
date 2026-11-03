package api

import "time"

// UpgradePayload asks an agent to replace its own binary. The agent downloads Path from the
// control URL it is connected to, checks SHA256 and that the binary reports Version, then a
// detached helper swaps it in, restarts the agent and puts the previous binary back when the
// new one does not reconnect in time.
type UpgradePayload struct {
	Version string `json:"version"` // version the new binary must report (the control server's)
	Path    string `json:"path"`    // /downloads/atoll-agent-linux-<arch>
	SHA256  string `json:"sha256"`  // hex SHA-256 of the hosted binary
}

// UpgradeStarted is the MsgUpgrade result output: the swap runs after the reply.
type UpgradeStarted struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Agent upgrade states.
const (
	AgentUpgradeRunning = "upgrading"
	AgentUpgradeDone    = "done"
	AgentUpgradeFailed  = "failed"
)

// AgentUpgrade is the last panel-started upgrade of one server's agent (stored by the control
// server, so it survives a restart).
type AgentUpgrade struct {
	State      string    `json:"state"` // upgrading | done | failed
	From       string    `json:"from"`
	To         string    `json:"to"`
	Error      string    `json:"error,omitempty"`
	RolledBack bool      `json:"rolled_back,omitempty"` // the new binary did not reconnect and the old one runs again
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
}
