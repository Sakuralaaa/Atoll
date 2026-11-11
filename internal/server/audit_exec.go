package server

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"strings"
)

// execAudit is what the audit log keeps of a `service.exec` command. The full argv often
// carries secrets (a psql password, a token in a curl header), and the audit log is readable
// by every admin and kept for a long time, so it records only the program, how many
// arguments followed, the shell flag when there is one, and a SHA-256 of the whole command:
// enough to tell commands apart and to match one an operator remembers, never the text.
type execAudit struct {
	Program string `json:"program"`
	Args    int    `json:"args"`
	Shell   string `json:"shell,omitempty"` // "-c" when the program is a shell running a script
	SHA256  string `json:"sha256"`
}

func auditExecDetail(cmd []string) execAudit {
	h := sha256.New()
	for _, a := range cmd {
		h.Write([]byte(a))
		h.Write([]byte{0})
	}
	d := execAudit{SHA256: hex.EncodeToString(h.Sum(nil))}
	if len(cmd) == 0 {
		return d
	}
	prog := path.Base(cmd[0])
	if len(prog) > 64 {
		prog = prog[:64]
	}
	d.Program, d.Args = prog, len(cmd)-1
	switch prog {
	case "sh", "bash", "ash", "dash", "zsh":
		if len(cmd) > 1 && strings.HasPrefix(cmd[1], "-") && strings.Contains(cmd[1], "c") {
			d.Shell = "-c"
		}
	}
	return d
}
