ingress_mode = "external"
shards = [
  { id="main", volume_type="csi", volume_source="vartovyi-archive-main", quota_bytes=1000000000000 },
  { id="second", volume_type="csi", volume_source="vartovyi-archive-second", quota_bytes=1000000000000 }
]
