[[ define "app-config" ]]
      env {
        VARTOVYI_CONFIG = "${NOMAD_SECRETS_DIR}/config.json"
[[ if ne .service "media" ]]
        OTEL_EXPORTER_OTLP_ENDPOINT = [[ var "otel_endpoint" .root | toJson ]]
        OTEL_TRACES_SAMPLER = "parentbased_traceidratio"
        OTEL_TRACES_SAMPLER_ARG = [[ var "otel_sample_ratio" .root | toJson ]]
[[ end ]]
      }
      template {
        destination = "secrets/config.json"
        uid = 10001
        gid = 10001
        perms = "0400"
        change_mode = "restart"
        data = <<EOH
{{ with nomadVar "nomad/jobs/[[ var "job_name" .root ]]" }}
{
  "service_token": {{ .service_token.Value | toJSON }},
  "listen": "[[ if eq .service "api" ]][[ if eq (var "api_network_mode" .root) "host" ]]0.0.0.0:{{ env "NOMAD_PORT_http" }}[[ else ]]:8080[[ end ]][[ else if eq .service "detector" ]]:8091[[ else if eq .service "notifier" ]]:8092[[ else ]]:8090[[ end ]]",
  "public_url": [[ var "public_url" .root | toJson ]],
  "cookie_secure": [[ var "cookie_secure" .root ]],
[[ if or (eq .service "api") (eq .service "recorder") (eq .service "notifier") (eq .service "migrate") ]]
  "encryption_key": {{ .encryption_key.Value | toJSON }},
[[ if var "postgres_enabled" .root ]]
  "database_host": "{{ range nomadService "[[ var "job_name" .root ]]-postgres" }}{{ .Address }}:{{ .Port }}{{ end }}",
  "database_password": {{ .postgres_password.Value | toJSON }},
[[ else ]]
  "database_url": {{ .database_url.Value | toJSON }},
[[ end ]]
[[ end ]]
[[ if or (eq .service "recorder") (eq .service "notifier") ]]
[[ if var "nats_enabled" .root ]]
  "nats_address": "{{ range nomadService "[[ var "job_name" .root ]]-nats" }}{{ .Address }}:{{ .Port }}{{ end }}",
  "nats_password": {{ .nats_password.Value | toJSON }},
[[ else ]]
  "nats_url": {{ .nats_url.Value | toJSON }},
[[ end ]]
[[ end ]]
[[ if eq .service "migrate" ]]
  "admin_user": "admin",
  "admin_password": {{ .admin_password.Value | toJSON }},
[[ end ]]
[[ if eq .service "api" ]]
  "oidc_issuer": [[ var "oidc_issuer" .root | toJson ]],
  "oidc_client_id": [[ var "oidc_client_id" .root | toJson ]],
  "oidc_role_claim": [[ var "oidc_role_claim" .root | toJson ]],
  "oidc_role_map": [[ var "oidc_role_map" .root | toJson ]],
  "oidc_secret": {{ if .oidc_secret }}{{ .oidc_secret.Value | toJSON }}{{ else }}""{{ end }},
  "discovery_interface": [[ var "discovery_interface" .root | toJson ]],
  "camera_cidrs": [[ var "camera_cidrs" .root | toJson ]],
[[ end ]]
[[ if eq .service "notifier" ]]
  "telegram_token": {{ if .telegram_token }}{{ .telegram_token.Value | toJSON }}{{ else }}""{{ end }},
  "telegram_chat": {{ if .telegram_chat }}{{ .telegram_chat.Value | toJSON }}{{ else }}""{{ end }},
[[ end ]]
  "shard_urls": {[[ range $i,$shard := var "shards" .root ]][[ if $i ]],[[ end ]]
    [[ $shard.id | toJson ]]: "http://{{ range nomadService "[[ var "job_name" $.root ]]-recorder-[[ $shard.id ]]" }}{{ .Address }}:{{ .Port }}{{ end }}"[[ end ]]
  },
  "shard_id": [[ .shard | toJson ]],
  "archive": "/archive",
  "quota_bytes": [[ .quota ]],
  "recorder_url": "http://127.0.0.1:8090",
  "media_url": "http://127.0.0.1:9997",
  "rtsp_url": "rtsp://127.0.0.1:8554",
  "hls_url": "http://127.0.0.1:8888",
  "playback_url": "http://127.0.0.1:9996"
}
{{ end }}
EOH
      }
[[ end ]]
[[ define "volume" ]]
    volume "data" {
      type = [[ .type | toJson ]]
      source = [[ .source | toJson ]]
      read_only = false
[[ if eq .type "csi" ]]
      attachment_mode = "file-system"
      access_mode = "single-node-writer"
[[ end ]]
    }
[[ end ]]
[[ define "mount" ]]
      volume_mount {
        volume = "data"
        destination = [[ . | toJson ]]
        read_only = false
      }
[[ end ]]
