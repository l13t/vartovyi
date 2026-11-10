#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
docker compose -f deploy/compose.test.yml -f deploy/compose.demo.yml up -d --build \
  api recorder detector notifier media source postgres nats demo
python3 scripts/demo-camera.py
