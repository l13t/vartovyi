# Architecture

## Services

`api` owns users, sessions and camera configuration. JSON and HTML handlers share
the same application operations and RBAC. The UI uses `html/template`, HTMX,
plain JavaScript modules, canvas zones and hls.js. Browser libraries are embedded
locally at image build time.

Each explicitly configured media shard has one `recorder`, one `detector` and
one dedicated MediaMTX instance sharing a Nomad allocation network. Recorder and
MediaMTX mount the same archive. Only recorder's authenticated HTTP endpoint is
advertised to other allocations; MediaMTX binds to loopback in Nomad.

```
RPi H.264/TCP -> FFmpeg stream-copy -> MediaMTX -> fMP4 archive
RTSP camera ------------------------> MediaMTX -> HLS / playback
                                         |
                                  FFmpeg grayscale
                                         |
                                    Go detector
                                         |
                              local recorder motion API
                                         |
                          PostgreSQL event + transactional outbox
                                         |
                               JetStream -> notifier -> Telegram

browser / future mobile -> api -> authenticated recorder media proxy
```

The detector never opens a second connection directly to the Pi. It reads a
MediaMTX substream, if configured, otherwise the main stream. Video bytes stay
outside NATS and PostgreSQL.

Detection starts automatically for enabled cameras and runs continuously in the
detector service. It does not depend on a browser, live playback, or an active
user session. Both continuous and motion recording modes detect motion; the mode
controls which recorded segments are retained. Events are finalized after the
configured post-motion interval expires.

## State and failure behavior

- Camera URLs are encrypted with AES-256-GCM. Session tokens are random and only
  their SHA-256 digests are persisted. Local passwords use PBKDF2-SHA256.
- Recordings have stable IDs from shard ID + relative path. An fsynced atomic
  manifest precedes acknowledgment of finalization. Duplicate hooks are safe.
- Recorder takes a filesystem lock, resets its dedicated MediaMTX paths before
  reconciliation, probes orphan MP4s, then restores sources. Unrecoverable files
  are preserved for inspection rather than silently indexed or removed.
- A private encrypted camera snapshot allows restart during a DB outage. Recording
  continues with last-known settings. Retention pauses until database recovery.
- Motion-only buffers at least 60 seconds, plus configured pre/post windows and
  finalization allowance. It retains complete segments that intersect events.
  Detector loss conservatively keeps footage instead of discarding it.
- MP4 exports operate serially per shard, with a five-minute deadline. Retention
  cannot run concurrently with an export. Requests containing gaps fail explicitly.
  The output starts at the first selected segment/keyframe, potentially before the
  requested start. Export files expire after 24 hours.
- Completed recording files are deleted only by recorder's age/quota policy.
  Active files and the current motion buffer are protected. Low filesystem space
  pauses recording; quotas cannot force deletion of active data.
- NATS uses persisted streams, explicit acknowledgments and stable message IDs.
  Outbox rollback may re-publish an event; consumers deduplicate by event ID.
  Telegram has no idempotent delivery primitive, so a timeout after acceptance can
  produce a duplicate. Delivery state/attempts are recorded; bursts are limited to
  one notification per camera per 30 seconds.

## Scale boundaries

The v1 index cache is in memory, backed by PostgreSQL and per-segment manifests.
It is bounded by the configured retention/quota, but recorder memory and startup
scan time grow with the retained segment count. This must be measured before a
32-camera production deployment; the default task resources are a starting point,
not a 32-camera sizing guarantee. Split cameras among stable shards as needed.

There is no automatic HA, camera reassignment or archive migration. A host volume
does not follow a task to a different node. A shared CSI volume must support
reliable filesystem locking and the requested writer access mode. PostgreSQL
and NATS each have one instance by default; external HA services can be supplied.

S3, AI inference, WebRTC and PTZ are outside v1. H.264 is the supported browser
profile. The implementation does not add software transcoding for H.265.
