// Package install embeds the install scripts so the control server can serve the exact
// scripts of its own version (GET /install-agent.sh, /install-server.sh) instead of
// whatever is on the main branch.
package install

import _ "embed"

//go:embed install-agent.sh
var AgentScript []byte

//go:embed install-server.sh
var ServerScript []byte
