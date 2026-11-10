# Vartovyi — Вартовий

A self-hosted video recorder written in Go, with a server-rendered HTMX UI,
client-independent JSON API, MediaMTX, FFmpeg, PostgreSQL and NATS JetStream.
Deployment is packaged as a **Nomad Pack**, with host/CSI archive volumes.

## Current state

This is the first implementation, **not a production-accepted release**. All Go
binaries compile, race-enabled unit tests and vet pass, Docker images build, and
the real Nomad Pack renderer validates all example configurations. The Docker
integration test passes recording, timeline, MP4 export/download and viewer access
checks with a synthetic RTSP camera. See the validation report for remaining
production acceptance checks. There is no published Vartovyi image yet.

See [validation status](docs/validation.md) for exactly what ran and
[deployment instructions](packs/vartovyi/README.md) for provisioning requirements.

## Features

- H.264/TCP input for existing Raspberry Pi `rpicam-vid` cameras; RTSP inputs and
  optional substreams; ONVIF discovery/profile reading.
- HLS live view, recording timeline, fMP4 playback and asynchronous MP4 export.
- Continuous and motion-only recording; polygon include/exclude zones, pre/post
  event recording, fail-open recording when the detector is unavailable.
- Local login, OIDC, admin/manager/viewer roles and Telegram notifications.
- Per-camera retention and archive quotas, durable manifests and reconciliation.
- JSON OpenAPI contract designed for future native clients; mobile OAuth/Bearer
  authentication and the native application are intentionally a future phase.

The target is one site with up to 32 cameras. Actual resource sizing and the
32-camera endurance test are still required. Start with one synthetic source,
then one real camera; preserve ZoneMinder until the pilot passes.

## Development

Requires Go 1.26+, FFmpeg/ffprobe and network access for dependencies/assets.

```sh
go mod tidy
make assets
make test
make vet
make build
make pack-check
```

Dependencies are locked in `go.mod` and `go.sum`. Use `go mod download` for an
unchanged checkout; run `go mod tidy` when changing dependencies and review both
lockfile changes. `go mod verify` checks downloaded module integrity.

For a disposable PostgreSQL/NATS/MediaMTX integration environment:

```sh
make integration
```

The test logs in, adds a synthetic RTSP camera, records video, reads the timeline,
exports and downloads MP4, and checks viewer permissions. Configuration under
`deploy/test` contains **test-only credentials**; never use it in production.
The API is published only on `127.0.0.1:8080`. The test archive is a disposable
Docker volume, independent of existing footage.

To keep the test environment running for manual UI inspection:

```sh
docker compose -f deploy/compose.test.yml up --build api recorder detector media source postgres nats notifier
```

Test login: `admin` / `admin`.

## Interactive demo

```sh
make demo
```

This keeps the local stack running at `http://localhost:8080` and registers
**Демо · Тестовий потік** with continuously generated H.264 video. The demo uses
one-day retention, a 250 MB camera quota, and no external notifications. Open the
camera, then its **Налаштування камери** tab to inspect connection, recording and
motion-zone controls. The main navigation has separate Cameras, Events, Users,
General settings and My profile destinations.

Demo login uses the disposable credentials above. If you changed that account's
credentials, supply `VARTOVYI_DEMO_USERNAME` and `VARTOVYI_DEMO_PASSWORD` to
`make demo`. To stop the demo without deleting its recordings:

```sh
docker compose -f deploy/compose.test.yml -f deploy/compose.demo.yml down
```

Automated `make integration` uses a unique project and a random localhost port;
it does not stop this manual stack or remove its archive.

## Build images

### Automated releases

GitHub Actions runs Go checks, integration tests, release tooling checks and real
Nomad Pack rendering/validation on pushes and pull requests. After all checks pass,
pushes to `master` run semantic-release using Conventional Commits:
`fix:` produces a patch, `feat:` a minor, and `BREAKING CHANGE:` or a `!` header
produces a major release. Commits such as `docs:` and `chore:` alone do not release.
Without an existing release tag, semantic-release starts at `1.0.0`. This
repository uses a bootstrap commit tagged `v0.0.0`, followed by
`feat: initial implementation`, so the first published release is `0.1.0`.

The release publishes `ghcr.io/l13t/vartovyi:<version>` and
`ghcr.io/l13t/vartovyi-media:<version>` for Linux amd64/arm64, then updates
`latest`, creates `v<version>` and publishes generated notes in GitHub Releases.
The workflow uses the built-in `GITHUB_TOKEN` with contents/packages write access;
no npm publication or additional registry secret is required. Repository/org
policies must allow those permissions and package creation. Pull requests never
publish. Release jobs are serialized without cancelling an active publication.

If publication fails, a versioned image may already exist; rerun the failed job
after correcting the cause. Both versioned builds must succeed before `latest`
promotion and Git tagging. Updates across two registry repositories and GitHub
are not transactional: a later failure may leave one `latest` updated or a Git
tag without a GitHub Release. Inspect the job log and registry before recovery.

### Local builds

```sh
docker build --target runtime -t vartovyi:dev .
docker build --target media -t vartovyi-media:dev .
```

Push your own immutable image tags/digests before deploying to a remote cluster.
For multiple architectures use `docker buildx build --platform linux/amd64,linux/arm64`
with each target. Nothing is pushed or deployed by the local build commands.

## Documentation

- [Architecture and data flow](docs/architecture.md)
- [Metrics and OpenTelemetry tracing](docs/observability.md)
- [Nomad Pack and secrets](packs/vartovyi/README.md)
- [Operations, restore and ZoneMinder cutover](docs/operations.md)
- [API and future mobile integration](docs/api.md)
- [OpenAPI JSON](internal/contract/openapi.json)
- [Validation and acceptance checklist](docs/validation.md)

### Administration

General settings at `/settings` are grouped into General, Recording, Camera groups, Telegram and
OIDC tabs. Administrators can configure OIDC from `/settings?tab=oidc`, including
the issuer, client credentials and group-to-role mapping. The page shows the exact
callback URL to register with the provider. Changes apply without restarting;
local password sign-in remains available. Client secrets and Telegram tokens are
encrypted and are never displayed after saving. See [API details](docs/api.md).

User search combines free text with `team: admin` and `user: admin*`; multiple
conditions use AND. Camera groups are managed at `/settings?tab=camera-groups`; clickable age/count/size
quotas at `/settings?tab=recording` open individual editors. The virtual `default`
group always includes every camera. Camera, group and all-camera limits apply cumulatively;
group/global size and count budgets are shared across recorder shards. Zero
means no limit at that scope. Oldest eligible finalized video files are removed
in the background; active/recent files remain protected.

The users, cameras and events lists support page sizes of 10, 25, 50 or 100.
Search and filters are retained when navigating; bulk user selection applies to
the displayed page.

For ESP32/MJPEG RTSP cameras, enable **Transcode to H.264 (MJPEG / ESP32)** in the
camera connection settings. RTSP transport defaults to TCP and can be switched to
UDP if required by the device/network. The saved stream URL is intentionally not
prefilled; the page confirms that it is stored, and a blank field preserves it.
