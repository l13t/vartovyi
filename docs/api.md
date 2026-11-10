# API and future mobile clients

The source contract is `internal/contract/openapi.json`, served to authenticated
users at `GET /api/v1/openapi.json`. `/api/v1` returns JSON and never HTMX fragments.
HTML routes and `/ui` actions are separate adapters over shared application logic.

## Web authentication

1. `GET /login` returns the login page and a short-lived login-CSRF cookie.
2. Submit `username`, `password` and the hidden `csrf` field to `POST /login`.
3. `GET /api/v1/me` returns the principal and session CSRF token.
4. Send `X-CSRF-Token` on API mutations. Cookie sessions expire after 24 hours;
   logout, password changes and role changes invalidate sessions.

OIDC uses `/auth/oidc` and `/auth/oidc/callback`, state, nonce and PKCE. Map a
configured role claim explicitly; new users otherwise become viewers. Existing
account roles remain controlled by Vartovyi administrators after initial mapping.

| Role | Permissions |
|---|---|
| viewer | Camera list, live, playback, events, snapshots, timeline |
| manager | Viewer permissions + cameras/zones/notifications, ONVIF, export |
| admin | Manager permissions + users, retention, quotas, camera deletion |

All three are installation-wide roles; per-camera ACLs are outside v1.

## Client contracts

- IDs are opaque stable strings; camera/shard IDs are safe ASCII identifiers.
- Times are RFC3339 UTC. Timeline/playback windows are at most 24 hours; export
  requests are at most one hour.
- Camera credentials are write-only. An empty main URL on update preserves the
  existing URL; substream `__unchanged__` preserves it, empty removes it.
- Camera writes require the current `revision`; conflicting writes return 409.
- New manager-created cameras use the default 14-day retention and no camera
  quota; only admins can alter those policies.
- Export creation requires `Idempotency-Key`; reusing it for a different request
  conflicts. Poll the returned job, then download the file when ready.
- Event pagination returns `items` and `next_cursor`. SSE sends full `status`
  snapshots; it is not a durable notification/event-history stream. On reconnect
  refresh event history through REST.
- Media URLs are relative routes through the API; clients never need camera
  passwords, internal recorder URLs or MediaMTX control endpoints.
- Errors have `{ "error": { "code": "...", "message": "..." } }` shape.
  Unauthenticated API/media access returns JSON 401 without login redirects.

## Own profile

Every authenticated role can use `GET /api/v1/profile` and
`PATCH /api/v1/profile`. Supported changes are `name`, `language`, `current_password` and
`new_password`; account ID, login and role are not editable through this route.
The web profile is at `/profile`, separate from administrator user management at
`/users` and installation settings at `/settings`.

Name changes take effect in existing sessions. Changing a local password requires
the current password, revokes all previous sessions, and issues a fresh session
cookie and CSRF token for the caller. The PATCH response returns `user` and
`csrf_token`; subsequent mutations must use that token. Accounts with no local
password advertise `can_change_password=false` and must manage their password
through their external identity provider.

Camera creation is at `/cameras/new`, with ONVIF tools linked at `/tools/onvif`.
The default creation page is a four-step wizard: preset, connection, recording
and motion, then review. Built-in presets cover RTSP/H.264, ESP32 MJPEG over RTSP
(server transcoding), raw Raspberry Pi H.264/TCP, and ONVIF profile selection.
Presets configure Vartovyi's input; they do not provision camera firmware or
guarantee a vendor-specific URL. All suggested ports, paths and input settings
remain editable. The manual form is at `/cameras/new?mode=manual`.

The wizard uses the existing manager-authorized ONVIF and camera JSON endpoints,
with session CSRF protection; there is no camera creation until final submission.
Its initial motion area is the whole frame, with zones available after creation.
Camera/group/global retention policies still apply. Credentials are URL-encoded
when supplied separately, omitted from review, kept out of URLs/browser storage
for wizard state, and cleared from the form when leaving. Selecting an ONVIF
profile does not test stream availability; the camera status does so after creation.

Camera playback and settings are separate pages: `/cameras/{id}` and
`/cameras/{id}/settings`. Events are at `/events` with a camera filter and cursor
pagination. These HTML routes use the same server-side role gates as the API.

## Native application boundary

The native application is not implemented. V1 reserves authentication as a
separate principal-producing adapter, so domain operations and role checks can
be reused with Bearer credentials. JSON and media routes are already independent
of the HTML UI.

The later mobile phase will implement authorization code + PKCE through the
system browser, with local/OIDC login at the server, short-lived access tokens,
rotating refresh sessions and per-device revocation. No public mobile token or
authorization endpoints are advertised as working in this release. Do not send
an arbitrary third-party OIDC access token to `/api/v1`; it is not accepted.

## Telegram settings and interface language

Administrators can GET/PUT `/api/v1/settings/telegram`. PUT accepts `enabled`,
`chat_id`, optional write-only `token` and `clear_token`. Empty/omitted tokens keep
an existing token; explicit removal takes precedence. Enabling requires both a
bot token and destination. GET/PUT responses include only `enabled`, `chat_id`
and `token_set`. Tokens are encrypted in PostgreSQL using the installation key.

The notifier resolves settings for each eligible event without restarting. When
no database row exists it uses the existing server Telegram configuration; once
saved, the database settings take precedence, including explicit disable/removal.
The admin form is in `/settings`. Cameras also need their `notify` flag enabled.
Saving settings does not send a test message. Destination format follows the
[Telegram Bot API](https://core.telegram.org/bots/api#sendmessage).

`PATCH /api/v1/profile` also accepts `language` (`uk` or `en`), independently for
each account. The header switch saves the preference, which applies in existing
sessions and persists across sign-ins. The login page offers both languages;
its preference is stored in a separate cookie. API field names, error codes and
user-entered names remain unchanged by interface translation.

## OIDC administration

Administrators manage `/api/v1/settings/oidc` with GET/PUT, or use the OIDC tab at
`/settings?tab=oidc`. General settings also have General, Recording and Telegram
tabs, each with its own URL and form. Telegram saves return to its own tab.

OIDC configuration includes `enabled`, `issuer`, `client_id`, optional write-only
`client_secret`, `role_claim`, `role_map` and `revision`. GET returns `secret_set`
instead of the secret. Empty/omitted secrets retain the current credential;
`clear_secret` removes it, supporting public clients with PKCE. Secrets are
AES-GCM encrypted in PostgreSQL. PUT requires the current GET revision and
returns 409 for a stale form. Enabling checks provider discovery before saving;
failed discovery leaves the stored configuration unchanged. Discovery does not
prove that the client credentials or registered redirect URI are correct.

Use an HTTPS issuer and register the exact callback URL shown in the UI:
`<public_url>/auth/oidc/callback`. HTTP is permitted only for loopback development.
The issuer and redirect requirements follow
[OpenID Connect Discovery](https://openid.net/specs/openid-connect-discovery-1_0.html).
Role claims are top-level strings or arrays of strings. The form accepts
comma-separated administrator and manager groups; unmatched new accounts become
viewers. Existing account roles remain managed in Vartovyi.

Stored settings override server OIDC configuration, including explicit disable.
API replicas resolve current settings for each sign-in and lazily cache provider
metadata. No restart is required. In-flight authorization flows are bound to the
configuration that started them; changing the issuer, credentials or role mapping
rejects the old callback. API startup and local password login do not require a
reachable identity provider. Existing user sessions survive OIDC configuration
changes; disabling OIDC prevents new OIDC sign-ins.

## Camera motion and account management

Camera motion settings have their own manager-only tab at `/cameras/{id}/motion`.
Saved zones are listed by name and type and drawn over the video. Zones can be
selected, renamed, redrawn or removed; changes apply after saving. Inclusion
zones are green, exclusion masks red. The editor excludes video letterboxing
when translating click coordinates. Threshold and pre/post event seconds are
saved together with the zones using camera revision checks. Connection settings
remain at `/cameras/{id}/settings`. The full camera JSON API still exposes these
fields for native clients; the existing `/ui/zones/{id}` adapter also accepts
optional `pre_seconds` and `post_seconds` fields.

`motion_enabled` controls background detection independently of camera recording.
Missing/null values preserve the existing value on updates; legacy cameras default
to enabled. When disabled, the detector stops and late signals are ignored.
Video capture continues; motion-mode cameras retain their video while detection
is disabled, subject to normal retention policies.

`motion_full_frame=true` analyzes the entire frame, ignoring all saved inclusion
zones and exclusion masks without deleting them. False uses saved zones/masks;
with no zones, detection also covers the whole frame. Both flags are accepted by
the JSON camera API and `/ui/zones/{id}` with the existing revision check. The
motion tab exposes an enable checkbox and an Entire frame / Drawn zones selector.
Motion changes do not reconnect the camera or interrupt its recording stream.

The `/users` search filters names, usernames and installation roles; `/users/new` and `/users/{id}` are
separate creation/editing pages. The browser generates a 24-character password
using `crypto.getRandomValues`, with 144 bits of randomness. A successfully saved
password can be viewed/copied on that page only; it is cleared when leaving and
is never returned by the users API, persisted in browser storage or recoverable
from the database password hash.

Administrators can POST `{"ids":["..."]}` to `/api/v1/users/bulk-delete` with CSRF.
The operation atomically deletes 1–1000 distinct accounts and their sessions.
It rejects the current caller, missing IDs and duplicate selections, and
serializes with role changes to retain an administrator. Recordings, camera
configuration and exports remain intact. The UI shows the selected usernames
before deletion. Removing an OIDC account does not revoke its identity-provider
access; an identity still authorized by that provider can sign in again.

The initial local account defaults to `admin` / `admin`. Bootstrap does not
replace existing passwords. Only this exact initial default bypasses the normal
12-byte minimum; newly created users and subsequent password changes retain the
existing password policy.

## User search

`/users?q=...` and `GET /api/v1/users?q=...` share the same query syntax. A blank
query lists everyone. Plain words match name, username or role (case-insensitive,
including Ukrainian role labels). `team: admin` matches the administrator role;
`user: admin*` matches usernames beginning with `admin`. Qualified values without
`*` match exactly; `*` is the only wildcard. Multiple terms use AND, and double
quotes group phrases. Invalid queries return 400 and never silently show everyone.
Bulk selection applies only to the current page of search results. The old role tabs have
been removed; their `?role=...` URLs still populate an equivalent `team:` query.

## Camera groups and scoped retention

Each camera can have one `group_id`, or an empty ID for ungrouped. Groups are
managed in the separate Camera groups settings tab; the dashboard filters cameras by group,
and camera connection forms assign membership. Group changes do not move files
or change recorder shards. Group limits include each member camera’s entire
existing archive. A group must be empty before deletion.

Administrators use GET/PUT `/api/v1/settings/recording` for `{revision, global,
groups}`. A group has `{id, name, limits}`. Each limit object has `max_age_days`,
`max_segments` and `max_bytes`. Zero disables that limit at that scope. Camera
limits remain in camera JSON (`retention_days`, `max_segments`, `quota_bytes`).
The Recording tab edits all three scopes and displays size in GiB. Local camera
connection edits preserve retention settings, even when quota fields are absent.

GET `/api/v1/camera-groups` returns group IDs/names to authenticated viewers;
POST creates or renames a group using the recording policy revision. DELETE
`/api/v1/camera-groups/{id}` takes a JSON revision and rejects groups in use.
Managers may assign cameras to existing groups; only administrators change quotas
or group definitions. Policy and camera updates use revision checks.

All limits apply cumulatively. A group’s count/size budget is shared by its
cameras; the global budget is shared across all configured cameras and shards.
The planner reads the common PostgreSQL catalog, removes expired files and camera
excess, then group excess, then global excess, retaining newer files first. Each
recorder removes only its own selected files, at most 1000 per pass. Passes run
about every 30 seconds and serialize with policy/membership updates and other
recorders, preventing a committed policy expansion from racing an old plan.

Only finalized video files are counted; count does not mean motion-event count.
Recent files and pre/post event buffers remain protected, so these are eventual
retention targets rather than strict write-blocking quotas. Metadata backlog or
an offline shard can delay convergence. The recorder’s configured physical-disk
quota and low-space protection remain independent. If the database or current
policy is unavailable, scoped pruning is skipped. Age limits also expire event
metadata/snapshots; count/size limits concern video files. Exports retain their
existing 24-hour expiry. Archives of deleted cameras remain explicit maintenance.

## Quota editors, default camera group and list pagination

`/settings?tab=recording` is a read-only quota overview. Names and individual
age/count/size values are links to a dedicated editor using the existing `scope`
query parameter. The editor shows the selected camera/group name, a back link
and cancel action; saving retains the existing revision checks and policy logic.
Missing scopes show the overview with an error, without editing global quotas.

Group management is separate at `/settings?tab=camera-groups`. Its immutable
`default` view automatically includes every camera, even cameras assigned to an
additional group. It is a virtual all-camera view, not a stored `group_id`; its
quota link edits the global policy. Explicit group creation/rename/deletion and
membership lists are separate from the quota editor. API group lists continue to
return stored additional groups only. No archive membership or quotas are changed
when viewing the default group.

Users, the camera dashboard and events have numbered HTML pagination using
`page` and `page_size` (10, 25, 50 or 100; default 10). Navigation retains filters
and search. The size control resets to page one. Out-of-range pages clamp to the
last available page; empty results show page one. Bulk selection applies only to
the displayed page. User search streams public database fields and keeps bounded
page buffers rather than limiting HTML searches to 1000 accounts. Event pages
count/query in PostgreSQL and retain a timestamp cutoff during navigation, so
normal newly detected events do not move earlier pages. The public JSON event
API retains its cursor contract; these numbered controls concern the web UI.

## MJPEG / ESP32 RTSP cameras

Camera JSON supports `transcode` (default false) and `rtsp_transport` (blank/TCP
by default, or `udp`). For MJPEG cameras, enable transcoding in connection settings:
recorder decodes the camera's RTSP stream with FFmpeg and publishes H.264/yuv420p
back to the private MediaMTX path. Encoding uses two threads, the configured FPS ceiling and a two-second keyframe
target. Input receive-wall-clock timestamps, reduced probing/buffering and
normalized variable-frame-rate PTS avoid inheriting an unreliable camera RTP
clock or synthesizing bursts of duplicate frames after delivery pauses. Internal publishing/reading remains
TCP. Regular RTSP sources retain direct proxying when transcoding is disabled;
raw Pi H.264/TCP retains stream copy.

This follows MediaMTX's documented
[FFmpeg re-encoding approach](https://mediamtx.org/docs/features/remuxing-reencoding-compression).
The camera URL stays encrypted and write-only. Its settings page now explicitly
confirms that the address is saved, separately from connection/recording status.
An empty URL field preserves the address; only enter a URL to replace it. Legacy
form submissions preserve the new connection options when those fields are absent.

The synthetic RTP/JPEG integration source uses standard Huffman tables and
`-force_duplicated_matrix 1` for MediaMTX compatibility. This is fixture encoding,
not an additional requirement imposed on real camera input, which FFmpeg reads
directly. `VARTOVYI_TEST_RUN=TestMJPEGTranscodingThroughPublicAPI make integration`
runs the focused codec/recording/HLS test in its own disposable Compose project.
