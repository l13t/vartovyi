CREATE TABLE IF NOT EXISTS api_users (
 id text PRIMARY KEY, username text UNIQUE NOT NULL, role text NOT NULL CHECK(role IN ('admin','manager','viewer')),
 password_hash text NOT NULL DEFAULT '', subject text UNIQUE
);
ALTER TABLE api_users ADD COLUMN IF NOT EXISTS display_name text NOT NULL DEFAULT '';
CREATE TABLE IF NOT EXISTS api_sessions (
 id text PRIMARY KEY, user_id text NOT NULL REFERENCES api_users(id) ON DELETE CASCADE,
 csrf text NOT NULL, expires timestamptz NOT NULL
);
CREATE TABLE IF NOT EXISTS api_cameras (id text PRIMARY KEY, config jsonb NOT NULL, revision bigint NOT NULL DEFAULT 1);
CREATE TABLE IF NOT EXISTS recorder_segments (
 id text PRIMARY KEY, camera_id text NOT NULL, shard_id text NOT NULL, path text NOT NULL,
 start_at timestamptz NOT NULL, end_at timestamptz NOT NULL, bytes bigint NOT NULL, keep boolean NOT NULL,
 UNIQUE(shard_id,path)
);
CREATE INDEX IF NOT EXISTS segments_camera_time ON recorder_segments(camera_id,start_at);
CREATE TABLE IF NOT EXISTS recorder_events (
 id text PRIMARY KEY, camera_id text NOT NULL, shard_id text NOT NULL, at timestamptz NOT NULL, payload jsonb NOT NULL
);
CREATE INDEX IF NOT EXISTS events_camera_time ON recorder_events(camera_id,at);
CREATE TABLE IF NOT EXISTS recorder_exports (
 id text PRIMARY KEY, user_id text NOT NULL, idempotency_key text NOT NULL, request_hash text NOT NULL,
 shard_id text NOT NULL, status text NOT NULL, payload jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(), UNIQUE(user_id,idempotency_key)
);
CREATE TABLE IF NOT EXISTS event_outbox (
 id text PRIMARY KEY, subject text NOT NULL, payload jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS notifier_deliveries (
 id text PRIMARY KEY, state text NOT NULL, attempts int NOT NULL DEFAULT 0,
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS recorder_status (camera_id text PRIMARY KEY, payload jsonb NOT NULL, updated_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS telegram_settings (
 id integer PRIMARY KEY CHECK(id=1), enabled boolean NOT NULL DEFAULT false,
 chat_id text NOT NULL DEFAULT '', token text NOT NULL DEFAULT ''
);

ALTER TABLE api_users ADD COLUMN IF NOT EXISTS language text NOT NULL DEFAULT 'uk' CHECK(language IN ('uk','en'));
CREATE TABLE IF NOT EXISTS oidc_settings (
 id integer PRIMARY KEY CHECK(id=1), config jsonb NOT NULL,
 client_secret text NOT NULL DEFAULT '', revision bigint NOT NULL DEFAULT 1
);
CREATE TABLE IF NOT EXISTS recording_settings(id integer PRIMARY KEY CHECK(id=1),config jsonb NOT NULL,revision bigint NOT NULL DEFAULT 1);

-- Trace metadata is private and independent of the public event payload.
ALTER TABLE event_outbox ADD COLUMN IF NOT EXISTS trace_context jsonb NOT NULL DEFAULT '{}';
