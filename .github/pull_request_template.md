## What and why

<!-- One or two sentences: the problem and the change. Link the issue if there is one. -->

## How it was tested

- [ ] `go vet ./... && gofmt -l . && go test ./...`
- [ ] `cd web && npm run lint && npm run build` (if web/ changed)
- [ ] `sh scripts/test-installers.sh` (if install/ changed)
- [ ] Tried on a real server (say which: OS, arch, Docker version)

## Review notes

- [ ] Database change: new migration file in `internal/server/store/migrations/`, never edits an old one
- [ ] Agent protocol change (`internal/api/protocol.go`): an older agent still works, or the upgrade order is written down
- [ ] New permission or endpoint: listed in `docs/api.md` and checked in `internal/server/perms.go`
- [ ] Anything that touches secrets, tokens or exec: no value reaches logs, argv or URLs
