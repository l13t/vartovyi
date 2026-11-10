[[ $root := . ]]
[[ if not (or (eq (var "ingress_mode" .) "bundled") (eq (var "ingress_mode" .) "external") (eq (var "ingress_mode" .) "disabled")) ]][[ fail "invalid ingress_mode" ]][[ end ]]
[[ if not (or (eq (var "api_network_mode" .) "bridge") (eq (var "api_network_mode" .) "host")) ]][[ fail "invalid api_network_mode" ]][[ end ]]
[[ if lt (len (var "shards" .)) 1 ]][[ fail "at least one shard required" ]][[ end ]]
[[ $seen := dict ]]
[[ range var "shards" . ]]
[[ if not (regexMatch "^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$" .id) ]][[ fail "invalid shard id" ]][[ end ]]
[[ if hasKey $seen .id ]][[ fail "duplicate shard id" ]][[ end ]][[ $_ := set $seen .id true ]]
[[ if not (or (eq .volume_type "host") (eq .volume_type "csi")) ]][[ fail "invalid shard volume_type" ]][[ end ]]
[[ if lt (int .quota_bytes) 0 ]][[ fail "negative archive quota" ]][[ end ]]
[[ end ]]
job [[ var "job_name" . | toJson ]] {
  type = "service"
  region = [[ var "region" . | toJson ]]
  namespace = [[ var "namespace" . | toJson ]]
  datacenters = [[ var "datacenters" . | toJson ]]
  node_pool = [[ var "node_pool" . | toJson ]]
  update {
    max_parallel = 1
    canary = 0
    min_healthy_time = "10s"
    healthy_deadline = "5m"
    progress_deadline = "10m"
    auto_revert = true
  }
  group "api" {
    count = [[ var "api_count" . ]]
    network {
      mode = [[ var "api_network_mode" . | toJson ]]
      port "http" { [[ if eq (var "api_network_mode" .) "bridge" ]]to = 8080[[ end ]] }
    }
    service {
      name = "[[ var "job_name" . ]]-api"
      provider = "nomad"
      port = "http"
      tags = [[ if ne (var "ingress_mode" .) "disabled" ]][[ list "traefik.enable=true" (printf "traefik.http.routers.%s.rule=Host(`%s`)" (var "job_name" .) (trimPrefix "https://" (trimPrefix "http://" (var "public_url" .)))) (printf "traefik.http.routers.%s.entrypoints=websecure" (var "job_name" .)) (printf "traefik.http.routers.%s.tls=true" (var "job_name" .)) | toJson ]][[ else ]][][[ end ]]
      check {
        type = "http"
        path = "/readyz"
        interval = "10s"
        timeout = "3s"
      }
    }
    task "migrate" {
      driver = "docker"
      kill_timeout = "30s"
      lifecycle { hook = "prestart" }
      config {
        image = [[ var "image" . | toJson ]]
        command = "/usr/local/bin/migrate"
      }
[[ template "app-config" (dict "root" . "service" "migrate" "shard" "" "quota" 0) ]]
      resources {
        cpu = [[ (index (var "resources" .) "api").cpu ]]
        memory = [[ (index (var "resources" .) "api").memory ]]
      }
    }
    task "api" {
      driver = "docker"
      kill_timeout = "30s"
      config {
        image = [[ var "image" . | toJson ]]
        command = "/usr/local/bin/api"
[[ if eq (var "api_network_mode" .) "host" ]]
        network_mode = "host"
[[ end ]]
        ports = ["http"]
      }
[[ template "app-config" (dict "root" . "service" "api" "shard" "" "quota" 0) ]]
      resources {
        cpu = [[ (index (var "resources" .) "api").cpu ]]
        memory = [[ (index (var "resources" .) "api").memory ]]
      }
    }
  }
[[ range $shard := var "shards" . ]]
  group "media-[[ $shard.id ]]" {
    count = 1
    network {
      mode = "bridge"
      port "recorder" { to = 8090 }
    }
    restart {
      attempts = 10
      interval = "10m"
      delay = "5s"
      mode = "delay"
    }
[[ template "volume" (dict "type" $shard.volume_type "source" $shard.volume_source) ]]
    service {
      name = "[[ var "job_name" $root ]]-recorder-[[ $shard.id ]]"
      provider = "nomad"
      port = "recorder"
      check {
        type = "http"
        path = "/healthz"
        interval = "10s"
        timeout = "3s"
      }
    }
    task "recorder" {
      driver = "docker"
      kill_timeout = "30s"
      config {
        image = [[ var "image" $root | toJson ]]
        command = "/usr/local/bin/recorder"
        ports = ["recorder"]
      }
[[ template "app-config" (dict "root" $root "service" "recorder" "shard" $shard.id "quota" $shard.quota_bytes) ]]
[[ template "mount" "/archive" ]]
      resources {
        cpu = [[ (index (var "resources" $root) "recorder").cpu ]]
        memory = [[ (index (var "resources" $root) "recorder").memory ]]
      }
    }
    task "detector" {
      driver = "docker"
      kill_timeout = "30s"
      config {
        image = [[ var "image" $root | toJson ]]
        command = "/usr/local/bin/detector"
      }
[[ template "app-config" (dict "root" $root "service" "detector" "shard" $shard.id "quota" $shard.quota_bytes) ]]
      resources {
        cpu = [[ (index (var "resources" $root) "detector").cpu ]]
        memory = [[ (index (var "resources" $root) "detector").memory ]]
      }
    }
    task "media" {
      driver = "docker"
      kill_timeout = "30s"
      config {
        image = [[ var "media_image" $root | toJson ]]
        command = "/usr/local/bin/mediamtx"
        args = ["/secrets/mediamtx.yml"]
      }
[[ template "app-config" (dict "root" $root "service" "media" "shard" $shard.id "quota" $shard.quota_bytes) ]]
[[ template "mount" "/archive" ]]
      template {
        destination = "secrets/mediamtx.yml"
        uid = 10001
        gid = 10001
        perms = "0400"
        change_mode = "restart"
        data = <<EOH
logLevel: warn
api: yes
apiAddress: 127.0.0.1:9997
rtsp: yes
rtspAddress: 127.0.0.1:8554
rtspTransports: [tcp]
rtmp: no
srt: no
moq: no
webrtc: no
hls: yes
hlsAddress: 127.0.0.1:8888
hlsCDNSecret: {{ with nomadVar "nomad/jobs/[[ var "job_name" $root ]]" }}{{ .service_token.Value | toJSON }}{{ end }}
playback: yes
playbackAddress: 127.0.0.1:9996
authMethod: http
authHTTPAddress: http://127.0.0.1:8090/internal/media-auth
authHTTPExclude: []
paths: {}
EOH
      }
      resources {
        cpu = [[ (index (var "resources" $root) "media").cpu ]]
        memory = [[ (index (var "resources" $root) "media").memory ]]
      }
    }
  }
[[ end ]]
  group "notifier" {
    network { mode = "bridge" }
    task "notifier" {
      driver = "docker"
      kill_timeout = "30s"
      config {
        image = [[ var "image" . | toJson ]]
        command = "/usr/local/bin/notifier"
      }
[[ template "app-config" (dict "root" . "service" "notifier" "shard" "" "quota" 0) ]]
      resources {
        cpu = [[ (index (var "resources" .) "notifier").cpu ]]
        memory = [[ (index (var "resources" .) "notifier").memory ]]
      }
    }
  }
[[ if var "postgres_enabled" . ]]
  group "postgres" {
    network {
      mode = "bridge"
      port "db" { to = 5432 }
    }
[[ template "volume" (var "postgres_volume" .) ]]
    service {
      name = "[[ var "job_name" . ]]-postgres"
      provider = "nomad"
      port = "db"
      check {
        type = "tcp"
        interval = "5s"
        timeout = "2s"
      }
    }
    task "postgres" {
      driver = "docker"
      kill_timeout = "60s"
      config {
        image = [[ var "postgres_image" . | toJson ]]
        ports = ["db"]
      }
      env {
        POSTGRES_DB = "vartovyi"
        POSTGRES_USER = "vartovyi"
        POSTGRES_PASSWORD_FILE = "/secrets/password"
        PGDATA = "/var/lib/postgresql/data/pgdata"
      }
[[ template "mount" "/var/lib/postgresql/data" ]]
      template {
        destination = "secrets/password"
        perms = "0400"
        data = "{{ with nomadVar \"nomad/jobs/[[ var "job_name" . ]]\" }}{{ .postgres_password }}{{ end }}"
      }
      resources {
        cpu = [[ (index (var "resources" .) "postgres").cpu ]]
        memory = [[ (index (var "resources" .) "postgres").memory ]]
      }
    }
  }
[[ end ]]
[[ if var "nats_enabled" . ]]
  group "nats" {
    network {
      mode = "bridge"
      port "nats" { to = 4222 }
    }
[[ template "volume" (var "nats_volume" .) ]]
    service {
      name = "[[ var "job_name" . ]]-nats"
      provider = "nomad"
      port = "nats"
      check {
        type = "tcp"
        interval = "5s"
        timeout = "2s"
      }
    }
    task "nats" {
      driver = "docker"
      config {
        image = [[ var "nats_image" . | toJson ]]
        user = "1000:1000"
        args = ["-c", "/secrets/nats.conf"]
        ports = ["nats"]
      }
[[ template "mount" "/data" ]]
      template {
        destination = "secrets/nats.conf"
        uid = 1000
        gid = 1000
        perms = "0400"
        data = <<EOH
port: 4222
jetstream { store_dir: /data/jetstream, max_file_store: 512MB }
authorization {
  users: [{ user: "vartovyi", password: {{ with nomadVar "nomad/jobs/[[ var "job_name" . ]]" }}{{ .nats_password.Value | toJSON }}{{ end }} }]
}
EOH
      }
      resources {
        cpu = [[ (index (var "resources" .) "nats").cpu ]]
        memory = [[ (index (var "resources" .) "nats").memory ]]
      }
    }
  }
[[ end ]]
[[ if eq (var "ingress_mode" .) "bundled" ]]
  group "ingress" {
    network {
      mode = "bridge"
      port "web" {
        static = [[ var "http_port" . ]]
        to = 80
      }
      port "websecure" {
        static = [[ var "https_port" . ]]
        to = 443
      }
    }
    task "traefik" {
      driver = "docker"
      config {
        image = [[ var "traefik_image" . | toJson ]]
        args = ["--configFile=/secrets/traefik.yml"]
        ports = ["web", "websecure"]
      }
      template {
        destination = "secrets/traefik.yml"
        data = <<EOH
entryPoints:
  web:
    address: ":80"
    http:
      redirections:
        entryPoint:
          to: websecure
  websecure:
    address: ":443"
providers:
  nomad:
    endpoint:
      address: [[ var "nomad_address" . | toJson ]]
      token: {{ with nomadVar "nomad/jobs/[[ var "job_name" . ]]" }}{{ .ingress_nomad_token.Value | toJSON }}{{ end }}
    namespace: [[ var "namespace" . | toJson ]]
    exposedByDefault: false
  file:
    filename: /secrets/tls.yml
EOH
      }
      template {
        destination = "secrets/tls.yml"
        data = <<EOH
tls:
  certificates:
    - certFile: /secrets/tls.crt
      keyFile: /secrets/tls.key
EOH
      }
      template {
        destination = "secrets/tls.crt"
        perms = "0400"
        data = "{{ with nomadVar \"nomad/jobs/[[ var "job_name" . ]]\" }}{{ .tls_cert }}{{ end }}"
      }
      template {
        destination = "secrets/tls.key"
        perms = "0400"
        data = "{{ with nomadVar \"nomad/jobs/[[ var "job_name" . ]]\" }}{{ .tls_key }}{{ end }}"
      }
      resources {
        cpu = [[ (index (var "resources" .) "ingress").cpu ]]
        memory = [[ (index (var "resources" .) "ingress").memory ]]
      }
    }
  }
[[ end ]]
}
