# Vartovyi Nomad Pack

Requires Linux Nomad clients with Docker, bridge CNI, native Nomad service
discovery and Nomad Variables. Target baseline: Nomad 1.9+ and Nomad Pack 0.4.2.
The pack deploys one job containing independent groups for API, media shards,
notifier, optional PostgreSQL/NATS, and optional Traefik.

## Provision before deployment

1. Build the `runtime` and `media` Dockerfile targets and make the images available
   on clients or in your registry. Set `image` and `media_image` accordingly.
2. Create the namespace and node pool. Provision PostgreSQL, NATS and archive
   volumes; the pack only mounts existing volumes.
3. For host archives, give UID/GID `10001:10001` ownership of the archive directory.
   PostgreSQL volumes must be writable by its image; NATS runs as UID/GID
   `1000:1000` and needs matching data-volume ownership.
4. Prepare DNS and a TLS certificate, or use your existing Traefik instance.
5. Limit the dynamically exposed DB, NATS and recorder ports to the cluster's
   private network. Camera network ranges must be explicitly allowed.

Example Nomad client host volumes (adjust paths to dedicated disks/datasets):

```hcl
host_volume "vartovyi-archive" {
  path = "/srv/vartovyi/archive"
  read_only = false
}
host_volume "vartovyi-postgres" {
  path = "/srv/vartovyi/postgres"
  read_only = false
}
host_volume "vartovyi-nats" {
  path = "/srv/vartovyi/nats"
  read_only = false
}
```

For CSI, register the volumes separately and set `volume_type = "csi"` and the
volume's source ID. The pack requests filesystem, single-node-writer access.
Use separate source IDs per shard. Mount a shared NAS through CSI for archives;
use local/block storage suitable for PostgreSQL for the database.

## Secrets

From the repository root:

```sh
python3 scripts/bootstrap-secrets.py --output /tmp/vartovyi-secrets.local.json
```

The generated file is mode 0600 and contains the Nomad Variables input object.
Read the initial administrator password from this file locally, and store it
and the encryption key in your password manager. It is not printed by the script.

Upload to the SAME namespace as the job:

```sh
nomad var put -namespace=default -in=json @/tmp/vartovyi-secrets.local.json
```

Default path: `nomad/jobs/vartovyi`. Changing `job_name` requires generating/uploading
the matching path (`--job <name>`). Job workload identities need template access
to their own job variable. All services share one job to use this native identity
scope; no long-lived Nomad token is provided to application services.

Required items:

| Item | Use |
|---|---|
| `service_token` | High-entropy internal HTTP/RTSP authentication |
| `encryption_key` | Base64-encoded 32-byte AES key; retain with backups |
| `admin_password` | Initial local admin; default `admin` (custom passwords: minimum 12 bytes) |
| `postgres_password` | When bundled PostgreSQL is enabled |
| `nats_password` | When bundled NATS is enabled |
| `database_url` | External PostgreSQL DSN when `postgres_enabled=false` |
| `nats_url` | External NATS URL when `nats_enabled=false` |

Optional items: `oidc_secret`, `telegram_token`, `telegram_chat`.

Bundled ingress additionally requires `tls_cert`, `tls_key` and
`ingress_nomad_token`. The ingress token needs only the Nomad read permissions
required by Traefik discovery in the selected namespace. Keep this token separate
from administrative credentials. TLS material is rendered to task secrets files.

## Render, inspect and run

```sh
nomad-pack render ./packs/vartovyi -f ./packs/vartovyi/examples/small-host.hcl
nomad-pack plan ./packs/vartovyi -f ./packs/vartovyi/examples/small-host.hcl
nomad-pack run ./packs/vartovyi -f ./packs/vartovyi/examples/small-host.hcl
```

The example uses external Traefik. Set `ingress_mode="bundled"` to deploy it;
set `nomad_address` to a Nomad API reachable from that allocation. `public_url`
must be an origin such as `https://cameras.example.com`, without a path suffix.
Traefik tags explicitly enable only the Vartovyi API, not internal services.

`ingress_mode="disabled"` publishes no ingress routes. To use plain HTTP for local
tests also set `cookie_secure=false`; deployed installations should use HTTPS.

`shards` configures stable IDs, volume type/source and archive quota in bytes.
The job-level `node_pool` controls placement; host volumes constrain scheduling
to nodes on which those volumes are available. `resources` controls each task.
The API can have multiple replicas; each media shard always has one allocation.

ONVIF multicast requires the API on the camera's network segment. Configure
`api_network_mode="host"` and `discovery_interface` to a real client interface
for discovery; ordinary bridge networking supports manually entered device URLs.
There is no automatic multicast forwarding across VLANs or sites.

Templates restart affected tasks when secrets/service endpoints change. Recorder
has no canary allocation; expect short gaps during updates. Task readiness does
not imply that all cameras are recording: inspect per-camera status and metrics.

## Examples

- `small-host.hcl`: one archive on a host volume, existing Traefik.
- `shared-csi.hcl`: two media shards with distinct CSI archive volumes.
- `external-services.hcl`: existing PostgreSQL, NATS and Traefik.

Destroying/stopping the pack does not delete registered volumes or recordings.
Changing a camera's shard is rejected until an explicit archive migration is
performed. Deleting a camera removes its configuration and stops its stream,
but does not purge old footage.

## OpenTelemetry

Set `otel_endpoint` to your OTLP/HTTP collector base URL and optionally
`otel_sample_ratio` (default `"0.1"`). Empty endpoint disables tracing. See
[the example](examples/observability.hcl) and
[observability documentation](../../docs/observability.md). The pack does not
deploy a collector or tracing backend.
