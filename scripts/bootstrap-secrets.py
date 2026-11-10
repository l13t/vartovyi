#!/usr/bin/env python3
"""Write a local Nomad Variables input file; never prints generated secrets."""
import argparse
import base64
import json
import os
import secrets

p = argparse.ArgumentParser()
p.add_argument("--job", default="vartovyi")
p.add_argument("--output", required=True)
a = p.parse_args()
items = {key: secrets.token_urlsafe(32) for key in ["service_token", "postgres_password", "nats_password"]}
items["admin_password"] = "admin"
items["encryption_key"] = base64.b64encode(secrets.token_bytes(32)).decode()
payload = {"Path": f"nomad/jobs/{a.job}", "Items": items}
fd = os.open(a.output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(fd, "w") as f:
    json.dump(payload, f, indent=2)
    f.write("\n")
print(f"Created {a.output} with mode 0600; add ingress/OIDC/Telegram settings before upload.")
