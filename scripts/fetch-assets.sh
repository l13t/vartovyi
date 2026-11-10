#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
dest=internal/web/static/vendor
curl --fail --silent --show-error --location --retry 3 --max-time 60 https://unpkg.com/htmx.org@2.0.11/dist/htmx.min.js -o "$dest/htmx.min.js"
curl --fail --silent --show-error --location --retry 3 --max-time 60 https://cdn.jsdelivr.net/npm/hls.js@1.7.4/dist/hls.min.js -o "$dest/hls.min.js"
