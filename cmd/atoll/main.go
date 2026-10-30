// Command atoll is the Atoll CLI: deploy and manage services, serve MCP over stdio
// (`atoll mcp`) and take over Zeabur-managed servers (`atoll zeabur`).
package main

import (
	"os"

	"github.com/Sakuralaaa/Atoll/internal/cli"
)

// version is set at build time: go build -ldflags "-X main.version=v0.1.0" ./cmd/atoll
var version = "dev"

func main() {
	os.Exit(cli.Execute(version))
}
