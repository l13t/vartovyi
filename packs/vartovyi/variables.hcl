variable "job_name" {
  type = string
  default = "vartovyi"
  description = "Nomad job and service prefix; unique per namespace."
}

variable "namespace" {
  type = string
  default = "default"
  description = "Existing Nomad namespace."
}

variable "region" {
  type = string
  default = "global"
  description = "Nomad region."
}

variable "datacenters" {
  type = list(string)
  default = ["dc1"]
  description = "Eligible datacenters."
}

variable "otel_endpoint" {
  type = string
  default = ""
  description = "Optional OTLP/HTTP collector base URL; empty disables tracing."
}

variable "otel_sample_ratio" {
  type = string
  default = "0.1"
  description = "Root trace sampling ratio from 0 to 1; parent sampling is respected."
}

variable "image" {
  type = string
  default = "vartovyi:dev"
  description = "Built application image; replace with immutable registry digest."
}

variable "media_image" {
  type = string
  default = "vartovyi-media:dev"
  description = "Built MediaMTX image containing the Go hook helper."
}

variable "postgres_image" {
  type = string
  default = "postgres:17.11-alpine"
  description = "PostgreSQL image."
}

variable "nats_image" {
  type = string
  default = "nats:2.15.2-alpine"
  description = "NATS JetStream image."
}

variable "traefik_image" {
  type = string
  default = "traefik:v3.7.17"
  description = "Optional ingress image."
}

variable "public_url" {
  type = string
  default = "https://vartovyi.example.com"
  description = "Public origin, also used for OIDC and origin checks."
}

variable "cookie_secure" {
  type = bool
  default = true
  description = "Require HTTPS cookies; false only for loopback development."
}

variable "camera_cidrs" {
  type = list(string)
  default = ["10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"]
  description = "Allowed network ranges for camera sources."
}

variable "discovery_interface" {
  type = string
  default = ""
  description = "IPv4 interface for ONVIF multicast; empty disables discovery."
}

variable "api_network_mode" {
  type = string
  default = "bridge"
  description = "Use host for ONVIF multicast; otherwise bridge."
}

variable "api_count" {
  type = number
  default = 1
  description = "API replicas."
}

variable "postgres_enabled" {
  type = bool
  default = true
  description = "Deploy a single PostgreSQL instance."
}

variable "nats_enabled" {
  type = bool
  default = true
  description = "Deploy a single NATS JetStream instance."
}

variable "postgres_volume" {
  type = object({ type = string, source = string })
  default = { type = "host", source = "vartovyi-postgres" }
  description = "Existing local/block volume for PostgreSQL."
}

variable "nats_volume" {
  type = object({ type = string, source = string })
  default = { type = "host", source = "vartovyi-nats" }
  description = "Existing persistent volume for JetStream."
}

variable "shards" {
  type = list(object({ id = string, volume_type = string, volume_source = string, quota_bytes = number }))
  default = [{ id = "main", volume_type = "host", volume_source = "vartovyi-archive", quota_bytes = 0 }]
  description = "Stable media shard IDs, existing volumes, and optional node pool."
}

variable "node_pool" {
  type = string
  default = "default"
  description = "Nomad node pool."
}

variable "ingress_mode" {
  type = string
  default = "bundled"
  description = "bundled, external (Traefik tags), or disabled."
}

variable "http_port" {
  type = number
  default = 80
  description = "Bundled ingress HTTP host port."
}

variable "https_port" {
  type = number
  default = 443
  description = "Bundled ingress HTTPS host port."
}

variable "nomad_address" {
  type = string
  default = "http://127.0.0.1:4646"
  description = "Nomad HTTP API reachable from bundled ingress."
}

variable "oidc_issuer" {
  type = string
  default = ""
  description = "Optional external OIDC issuer."
}

variable "oidc_client_id" {
  type = string
  default = ""
  description = "OIDC web client ID."
}

variable "oidc_role_claim" {
  type = string
  default = "roles"
  description = "Claim used only for initial account role mapping."
}

variable "oidc_role_map" {
  type = map(string)
  default = {}
  description = "Explicit external claim value to admin/manager/viewer mapping."
}

variable "resources" {
  type = map(object({ cpu = number, memory = number }))
  default = { api = {cpu=1000,memory=256}, recorder={cpu=1000,memory=512}, detector={cpu=2000,memory=512}, media={cpu=1000,memory=512}, notifier={cpu=250,memory=128}, postgres={cpu=1000,memory=512}, nats={cpu=500,memory=256}, ingress={cpu=250,memory=128} }
  description = "Per-task Nomad resource limits."
}
