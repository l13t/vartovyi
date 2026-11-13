# Dependency update — 2026-11-10

Versions were checked against the Go module proxy, npm registry, Docker Hub and
upstream GitHub releases. Go module versions and checksums are in `go.mod` and
`go.sum`; release tooling is locked in `package-lock.json`.

| Component | Previous | Updated |
| --- | --- | --- |
| Build/CI Go | 1.25.0 | 1.27.3 |
| Minimum source Go | 1.25.0 | 1.26.0 |
| Runtime distribution | Debian bookworm | Debian trixie |
| PostgreSQL | 17.2 | 17.11 |
| NATS server | 2.10.24 | 2.15.2 |
| BusyBox archive initializer | 1.37.0 | 1.38.0 |
| Optional Traefik | 3.3.2 | 3.7.17 |
| CI Nomad | 1.9.5 | 2.1.1 |
| HTMX | 2.0.4 | 2.0.11 |
| hls.js | 1.5.20 | 1.7.4 |
| OIDC Go client | 3.12.0 | 3.21.0 |
| pgx | 5.7.2 | 5.12.0 |
| NATS Go client | 1.39.1 | 1.55.0 |
| oauth2 | 0.25.0 | 0.38.0 |
| OpenTelemetry | newly added | 1.47.0 |
| semantic-release | 25.0.2 | 25.0.9 |
| Conventional Commits preset | 9.0.0 | 9.3.1 |
| Release Node.js | 24.10.0 | 26.13.0 |

GitHub Actions are pinned to release tags in `.github/workflows/ci.yml`.
Nomad CLIs are installed from versioned official archives with SHA-256 checks,
avoiding the Node 20 runtimes declared by the HashiCorp setup actions. Release
Node.js is shared through `.node-version`.

Conventional Commits preset 10.x requires changelog writer 9+, while the installed
release-notes-generator 14.1.1 uses writer 8. Preset 9.3.1 is pinned for
compatibility; tests exercise both commit analysis and actual notes generation.
MediaMTX 1.21.3 and Nomad Pack CLI 0.4.2 were already current and remain pinned.
Go transitive dependencies were also updated to their current compatible releases.

PostgreSQL stays on major 17 to preserve existing volume compatibility. Existing
volumes are never upgraded or removed by these edits. Deployments still require
normal backup and acceptance procedures. FFmpeg follows the distribution's
supported package rather than an independently compiled upstream release.

Primary version sources include [Go downloads](https://go.dev/dl/),
[MediaMTX releases](https://github.com/bluenviron/mediamtx/releases),
[NATS releases](https://github.com/nats-io/nats-server/releases),
[PostgreSQL images](https://hub.docker.com/_/postgres), and
[Nomad releases](https://github.com/hashicorp/nomad/releases).
See `docs/validation.md` for checks actually executed after this update.

## Dependabot security update — 2026-11-13

The managed Dependabot security job could not replace vulnerable minimatch/glob
inside npm's bundled dependency tree. Leaf overrides were tested and did not
replace those bundled copies. Vartovyi does not use npm package publishing, so
`@semantic-release/npm` is replaced using a root file dependency/override with
`scripts/disabled-npm-publisher`. This local plugin explicitly fails verification
if enabled; the configured GitHub and container publishers are unchanged.
`.npmrc` sets `install-links=true` so `npm ci` installs the local package
reproducibly rather than relying on directory links.

The lockfile no longer contains bundled npm, minimatch or glob. A clean `npm ci`,
all six release tests (including real changelog generation and the disabled
publisher guard), and `npm audit` passed locally with zero reported
vulnerabilities. CI now audits development/release dependencies at moderate
severity and above. No advisories are suppressed. This replacement can be
removed when upstream semantic-release makes the unused npm publisher optional,
or its bundled dependency tree is fixed and re-audited.
