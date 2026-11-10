# Validation status

## Update verified on 2026-11-10

- Go modules and release tooling were updated and locked; `go mod verify`
  passed. Builds now use Go 1.27.3 and Debian trixie; source requires Go 1.26+.
  PostgreSQL stays on major 17 (17.11). NATS is 2.15.2; browser assets are HTMX
  2.0.11 and hls.js 1.7.4. See [dependency update details](dependency-updates.md).
- `go test -race ./...`, `go vet ./...`, release tooling tests (4), UI module
  tests (5), workflow YAML parsing and plugin loading passed locally.
- Optional OTLP/HTTP tracing tests verify protobuf export to a synthetic HTTP
  collector, flush on shutdown, bounded shutdown with an unavailable collector,
  nonblocking request handling under queue pressure, parent-based sampling,
  HTTP context propagation/streaming, route redaction and SQL/error privacy.
  No production collector or tracing backend was contacted.
- Full disposable Docker integration passed with the updated runtime, PostgreSQL
  and NATS. Tests include durable outbox context restoration using a fresh Store,
  real JetStream headers and a synthetic consumer (also rerun after the final
  persistence span change), as well as the existing
  recording, motion, HLS, export, OIDC, settings and permission regression.
- The update exposed FFmpeg's rejection of `-r` with VFR. The output frame-rate
  override was removed; the real MJPEG-to-H.264 recording/HLS test passes.
  W3C headers are written as lowercase NATS keys and consumed case-insensitively.
- `make pack-check` passed real rendering and jobspec validation for defaults,
  external services, shared CSI, small host and the new observability example.
  Warnings about unavailable driver validation and shutdown delays remain;
  no Nomad runtime deployment was attempted.
- Web template rendering passed with an explicit test version injected via
  `-ldflags`. Release image builds pass the semantic version into the binary.
  License selection remains pending; the footer currently says it is unspecified.
  Browser visual acceptance for the updated browser libraries was not run.
- Container builds were exercised locally for Linux arm64. The amd64/arm64
  release workflow was not executed on GitHub, and no images were published.
  No commit/push, live camera access or changes to existing footage occurred.

## Earlier verification snapshot

Verified on 2026-11-04: macOS arm64, Go 1.27.3, FFmpeg, Nomad 2.1.1,
Nomad Pack 0.4.2 and Docker Engine 29.8.2 (Linux arm64). Container builds used
Go 1.25.0 at that time. The source then targeted Go 1.25+ and Linux deployment.

## Passed

- Camera setup wizard and built-in presets (2026-11-05): RTSP/H.264, ESP32
  RTSP/MJPEG transcoding, Pi raw H.264/TCP, and ONVIF profile selection. Tests
  cover preset URLs, credential encoding and review redaction, manager creation
  via the existing API, manual-form fallback and bilingual template rendering.
  Headless Chrome checks passed for all presets, required-field navigation,
  back navigation, final creation payload/redirect, and mobile overflow; desktop
  and mobile screenshots were inspected. Browser camera creation and ONVIF
  device responses were mocked; actual persistence was checked separately in
  disposable Compose. No cameras were added to the persistent demo during QA.
- Motion enable/disable and full-frame selection (2026-11-05): race tests and
  vet pass; a synthetic-camera Compose test verifies that disabling stops
  background detector heartbeats while recording continues, and re-enabling
  full-frame detection generates motion despite a preserved full-image exclusion
  mask. Pixel tests cover saved-area versus full-frame scoring. HTTP checks of
  the refreshed demo confirm the new controls, playback and archive availability.
  Browser interaction/visual acceptance remains pending as described below.
- Dependency download, generation of `go.sum`, and `go mod verify`.
- Pinned HTMX/hls.js assets downloaded for embedding in the application.
- `go test -race ./...`: all packages compile and all unit tests pass. The
  integration test is gated in this invocation and was run separately in Docker.
- `go vet ./...` and `go build -o bin/ ./cmd/...`: all six service/helper binaries.
- Password hashing, credential encryption/tamper rejection, motion include/exclude
  zones, archive ownership, private manifest persistence, path confinement,
  idempotent finalization and MediaMTX timezone parsing (`Z` and numeric offsets).
- FFmpeg-generated H.264 fixtures, ffprobe duration, MP4 export and HTML escaping.
- `make pack-check`: actual Nomad Pack rendering and `nomad job validate` for
  defaults, external services, shared CSI and small-host configurations.
- Offline pack/runtime-template tests, including API host networking and private
  JSON configuration rendering. The real renderer caught missing app metadata
  and an int/float comparison; both are fixed.
- Docker runtime/media image builds for Linux arm64.
- Verified on 2026-11-05: RTSP/MJPEG transcoding to H.264 passes a real-container
  synthetic test, including finalized recordings, HLS playlists/media fragments,
  stored-address feedback and preservation of connection options in legacy forms.
  Full integration regression passes; the focused test also passes after receive
  wall-clock/VFR normalization and removal of duplicate-frame catch-up bursts.
  A real authorized ESP32 stream was checked through HLS and its archive: a
  60-second observation produced six new finalized recordings with no new clock
  drift resets. Camera URL values remain encrypted/write-only.
- Clickable quota overview opens separate scoped editors. Group management is a
  separate tab; its immutable virtual default view includes cameras assigned to
  additional groups as well as cameras with no additional group. Bilingual
  rendering and real HTTP integration checks cover panel separation and membership.
- Users, cameras and events: pagination sizes 10/25/50/100 pass integration
  checks with synthetic rows. Search/filter retention, last-page clamping and the
  last event page are verified. User HTML pagination streams public fields with
  bounded buffers; existing JSON event cursors remain supported.
- User search supports plain names/logins/roles, `team:` and `user:` predicates,
  case folding, wildcard patterns, quoted phrases and AND combinations. Invalid
  queries are rejected. Role tabs have been replaced by a shareable search field.
- Camera groups and recording policy tests cover membership, nonempty-group
  deletion rejection, native forms for camera/group/global limits and permissions.
  Synthetic shared-catalog tests span two shard IDs and verify age, count, size,
  protected files and camera/group/global planning order. No real footage is
  deleted during these tests. Actual multi-node failure/load behavior remains
  part of the deployment acceptance checks below.
- Separate camera motion panel and visible saved-zone list: bilingual template
  checks, zone round-trip/edit/removal, pre/post motion settings, role gates and
  revision conflict checks pass. Node tests cover normalized polygons,
  letterboxing coordinates and secure password generation; JS syntax checks pass.
- User search, separate creation/edit pages and atomic bulk deletion pass
  integration checks, including missing/duplicate/self selections and session
  revocation. User passwords remain write-only; the creation UI retains a new
  password in memory only until leaving the page. Default `admin` / `admin`
  bootstrap passes while ordinary password creation retains the 12-byte minimum.
- Settings tabs and dynamic OIDC administration: bilingual panel rendering,
  validation, secret preservation/encryption/redaction and role permissions pass.
  A local synthetic provider exercises discovery, a real RSA-signed ID token,
  state/nonce/PKCE, dynamic client ID and group mapping, a fresh API replica,
  failed-discovery rollback, stale revisions, rejection of an in-flight login
  after configuration changes, HTML disable and continued local password login.
  No external identity provider or real accounts were contacted.
- Telegram settings and English/Ukrainian interface: race tests and vet pass.
  Integration checks verify administrator-only access, encrypted/write-only bot
  tokens, preservation/removal semantics and disabling server-config fallback.
  Language changes persist across sign-ins, reject unsupported values and affect
  only the current account. English template checks preserve user-entered names.
  A mock Telegram HTTP transport verifies resolved credentials and sanitized
  errors; no real Telegram messages were sent. JavaScript syntax checks pass.
- `make integration`: **passed** against real PostgreSQL, NATS and MediaMTX
  containers. Coverage includes profile updates, password rotation and session
  revocation, protected identity fields, local-password rejection for an OIDC-only
  account, and viewer/manager/admin page permissions. The video test verifies
  RTSP recording, authenticated HLS playlists and a real media fragment, detector
  heartbeat, public JSON timeline, MP4 export/download and viewer mutation denial.
  Background detection is explicitly verified: the viewing user signs out before
  the synthetic stream starts, and a motion event must originate during that
  signed-out interval, before any HTML camera page or HLS request is made.
- The persistent `make demo` stack serves a synthetic camera with live HLS,
  recorded segments, a motion event and its snapshot. HTTP checks passed for the
  separate camera, camera settings, events, users, general settings, ONVIF,
  add-camera and personal profile pages. Browser visual acceptance remains pending:
  the browser automation host was unavailable in this session.

The integration run found a dynamic-configuration crash in MediaMTX 1.11.3.
The image is now pinned to 1.21.3, with the required `%path` recording placeholder
and explicit timezone handling. Unused Media-over-QUIC listeners are disabled.

Integration runs use unique Compose projects and dynamically allocated ports;
their containers and dedicated disposable archive volumes are removed afterward.
The separate local demo stack remains running, with its archive preserved.
Local development images remain available. No live cameras, existing
footage, external Nomad cluster or messaging accounts were modified. No images
were published and no Git commit/push was performed.

## Remaining acceptance checks

1. Deploy the pack to a Linux Nomad test cluster to exercise actual Docker driver
   configuration, CNI, service discovery, workload identity, Nomad Variables and
   volume permissions. Local jobspec validation warns that driver configuration
   cannot be checked without a running Nomad agent; it also reports missing
   shutdown delays for service groups.
2. Test OIDC with a real test provider, ONVIF devices and Telegram with a test bot.
   Starting NATS in the integration test does not prove every delivery/retry path.
3. Run browser acceptance for live playback, HTMX forms, timeline, drawing zones,
   export and mobile-width layouts; verify status updates preserve active video.
4. Exercise H.264/TCP on the existing Pi setup and inject DB/NATS/detector/stream
   outages, filled/unmounted archives, restarts and duplicate deliveries.
5. Run the 32-stream, 24-hour load test on identified hardware and publish CPU,
   RAM, disk, indexing lag and latency measurements. Recorder's retained-segment
   cache makes sizing important for long retention periods.
6. Complete the one-camera then 3–4-camera 72-hour pilot before replacing ZoneMinder.
