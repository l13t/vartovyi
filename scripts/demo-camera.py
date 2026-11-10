#!/usr/bin/env python3
"""Register one synthetic camera via the same authenticated API as the UI."""
import http.cookiejar
import json
import os
import pathlib
import re
import time
import urllib.error
import urllib.parse
import urllib.request

root = pathlib.Path(__file__).resolve().parents[1]
config = json.loads((root / 'deploy/test/api.json').read_text())
base = os.environ.get('VARTOVYI_DEMO_URL', 'http://localhost:8080').rstrip('/')
username = os.environ.get('VARTOVYI_DEMO_USERNAME', config['admin_user'])
password = os.environ.get('VARTOVYI_DEMO_PASSWORD', config['admin_password'])
client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
for attempt in range(40):
    try:
        with client.open(base + '/readyz', timeout=3) as response:
            if response.status == 200:
                break
    except (OSError, urllib.error.URLError):
        time.sleep(1)
else:
    raise SystemExit('Vartovyi did not become ready')
with client.open(base + '/login', timeout=5) as response:
    page = response.read().decode()
csrf = re.search(r'name="csrf" value="([^"]+)"', page)
if not csrf:
    raise SystemExit('Login page did not provide CSRF token')
body = urllib.parse.urlencode({'username': username, 'password': password, 'csrf': csrf.group(1)}).encode()
try:
    with client.open(base + '/login', data=body, timeout=10) as response:
        response.read()
except urllib.error.HTTPError as exc:
    raise SystemExit(f'Demo login failed (HTTP {exc.code}); set VARTOVYI_DEMO_USERNAME/PASSWORD if changed')
with client.open(base + '/api/v1/me', timeout=5) as response:
    principal = json.load(response)
try:
    with client.open(base + '/api/v1/cameras/demo-camera', timeout=5) as response:
        response.read()
    print(f'Demo camera already exists: {base}/cameras/demo-camera')
except urllib.error.HTTPError as exc:
    if exc.code != 404:
        raise SystemExit(f'Cannot inspect demo camera: HTTP {exc.code}')
    camera = {'id': 'demo-camera', 'name': 'Демо · Тестовий потік', 'shard_id': 'main',
              'source': 'rtsp', 'url': 'rtsp://source:8554/synthetic', 'fps': 10,
              'enabled': True, 'mode': 'continuous', 'retention_days': 1,
              'quota_bytes': 250_000_000, 'pre_seconds': 10, 'post_seconds': 20,
              'threshold': 0.02, 'zones': [], 'notify': False, 'revision': 0}
    request = urllib.request.Request(base + '/api/v1/cameras', data=json.dumps(camera).encode(),
                                    headers={'Content-Type': 'application/json',
                                             'X-CSRF-Token': principal['csrf_token']}, method='POST')
    try:
        with client.open(request, timeout=10) as response:
            response.read()
    except urllib.error.HTTPError as exc:
        raise SystemExit(f'Cannot create demo camera: HTTP {exc.code}')
    print(f'Demo camera ready: {base}/cameras/demo-camera')
