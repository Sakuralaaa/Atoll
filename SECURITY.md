# Security Policy

## Report a vulnerability privately

Please use GitHub's [private vulnerability reporting form](https://github.com/Sakuralaaa/Atoll/security/advisories/new). Private vulnerability reporting is enabled for this repository. **Do not open a public issue or discussion containing exploit details, credentials, tokens, or user data.**

If the private report form is unavailable, open an issue that only asks for a private contact path; do not include vulnerability details, indicators, credentials, or user data in public.

A useful report includes the affected release or commit, deployment type and environment, security impact, minimal reproduction steps, and any relevant logs after secrets and personal data have been removed. Please avoid testing against infrastructure you do not own or have permission to test.

## Scope

The policy covers Atoll's control server, agent, CLI, web panel, installers, and repository automation. Atoll is currently marked **pre-alpha**. When practical, include whether the issue reproduces on the latest release or the current default branch.

## Supported versions / 支持的版本

| Version | Security fixes |
| --- | --- |
| Latest stable release (`vX.Y.Z`) | ✅ Fixes ship in a new patch or minor release |
| Older releases ≥ v0.1.40 | ❌ Upgrade directly to the latest stable release (supported, see [docs/compatibility.md](docs/compatibility.md)) |
| Pre-releases (`-beta.N`) | ❌ Best effort only; move to the next stable release |
| < v0.1.40 | ❌ Unsupported |

Agents are supported from v0.1.40 up to the control server's own version; upgrade them from the panel after updating the control server. There are no backports to older release lines and no response-time SLA at this time. The latest self-review is in [docs/security-review-2026-11.md](docs/security-review-2026-11.md).

只有最新正式版接收安全修复；任意 ≥ v0.1.40 的版本可直接升级到最新正式版。详见 [docs/compatibility.md](docs/compatibility.md)。

## Coordinated disclosure

Please give maintainers a reasonable opportunity to assess and address a report before public disclosure. There is no fixed response or remediation timeline at this time. This policy is not a promise that every report will result in a release or a particular fix date.
