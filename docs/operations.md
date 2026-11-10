# Operations and cutover

## Initial setup

Deploy only after the full build and disposable integration checks pass. Keep
recordings on dedicated provisioned volumes. Login as the bootstrapped `admin`,
change its password in Settings, and retain the encryption key securely.

Configure each Pi as `h264_tcp`, for example `tcp://camera-name:8554`, with the
actual camera FPS. The existing Ansible defaults are H.264, 10 FPS, 1.5 Mbps;
one host override is 5 FPS, 1 Mbps. This is repository configuration, not live
device verification. No camera reconfiguration is included in this change.

The default six-second keyframe interval affects time-to-first-frame and HLS
latency. Measure it before changing GOP or enabling a second stream on a Pi.
Analysis runs centrally, not on the Pi. ONVIF discovery does not apply to the
current raw-TCP Pi endpoints.

## Storage planning

Continuous 1.5 Mbps video is approximately 16.2 GB/day per camera before overhead:
about 907 GB for four cameras over 14 days, or 7.26 TB for 32. These are decimal
capacity estimates, not measured disk usage. Allow filesystem/headroom and
metadata space. Quotas may shorten the maximum retention period.

The 14-day setting is a maximum age, not a guarantee that footage survives a
smaller configured quota. Recorder cleans oldest completed files first. It
protects active segments and the pre/post buffer, and pauses recording if the
quota/free-space condition cannot be resolved. Inspect camera status for
`disk_full`, `offline` or `detector_degraded` instead of relying on process liveness.

Exports are available for 24 hours. Event snapshots follow event retention.
Deleting a camera preserves its old recording files; deliberate purging of such
an archive is an operator maintenance action, not a side effect of deleting UI
configuration.

## Backups and recovery

Back up PostgreSQL, Nomad configuration/Variables (especially `encryption_key`),
and archive volumes. A database backup without the encryption key cannot restore
camera credentials. A backup without footage cannot restore recordings.

Use PostgreSQL's normal backup tooling for the selected deployment; for a simple
v1 installation a regular logical `pg_dump` plus tested restore is sufficient.
Coordinate archive snapshots with recorder shutdown when a consistent cut is
required. NATS JetStream's volume preserves queued notifications across restarts.

Restore procedure:

1. Stop the Vartovyi job; preserve all volumes and the existing DB backup.
2. Restore PostgreSQL, the original encryption key/configuration, and archives
   under the same stable shard IDs and volume sources.
3. Start the same application/image version against the restored state.
4. Recorder stops sources in its dedicated MediaMTX instance, loads manifests,
   probes orphan MP4s, reconstructs segment/event metadata, then resumes sources.
5. Check per-camera status and play historical plus newly recorded intervals.

Restore does not recreate deleted media. If a damaged final segment cannot be
probed, it is preserved for manual inspection. Reconciliation does not prove
that the original stream contained no gaps.

Do not rotate `encryption_key` by editing it in place: existing ciphertext will
become unreadable. A transactional re-encryption migration is required and is
outside v1. Rotating `service_token` restarts dependent tasks; expect a gap.

## Upgrades and rollback

Pin immutable app and media image digests. Back up before upgrade. Migrations
run under a PostgreSQL advisory lock; each API allocation has a prestart migrator.
The initial migration is idempotent. Future schema changes must be additive and
compatible with the previous image before promising automatic rollback.

Roll media shards one at a time. Each shard has one recorder and no canary, so
updates introduce a short recording interruption. Reverting an image does not
revert database schema or resurrect files removed by retention.

## Monitoring

Scrape authenticated `/metrics` on the API and recorder services, passing the
internal token via a private Prometheus credentials file. Do not place tokens in
dashboard URLs. Use the service names from the pack for discovery; do not expose
MediaMTX control/metrics ports publicly.

Import `monitoring/dashboard.json` and `monitoring/alerts.yml` into the existing
stack. For recorder-down alerts, label the scrape job `vartovyi-recorder` or adapt
the selector. Metrics include recording freshness, detector heartbeat, queued
segment indexing, outbox backlog, repeated notification failures and disk pause.

Optional OpenTelemetry tracing exports over OTLP/HTTP to your collector. See
[observability configuration](observability.md) for environment variables,
sampling, privacy and failure behavior. Prometheus scraping remains independent.

## ZoneMinder migration

1. Back up ZoneMinder configuration and retain its archive read-only.
2. Validate Vartovyi with the synthetic input before using a live device.
3. Move one Pi connection to Vartovyi. Avoid two independent readers competing
   for the Pi's raw TCP listener.
4. If concurrent comparison is required, temporarily configure ZoneMinder to
   consume an authenticated MediaMTX RTSP relay. This requires an explicitly
   configured private-network RTSP listener; the default pack binds it to loopback.
5. Check live view, continuous/motion recording, day/night noise, zones, timestamps,
   pre/post windows, export and Telegram. Disconnect/reconnect the camera and
   restart components to confirm recovery.
6. Add the remaining cameras, run the 72-hour pilot, and disable ZoneMinder's
   recording only after acceptance. Keep its old archive available separately.

There is no ZoneMinder schema/API compatibility or automatic historical import.
