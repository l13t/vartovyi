#!/usr/bin/env python3
"""Render with the real nomad-pack CLI, then validate every jobspec locally."""
import pathlib
import subprocess
import tempfile

root = pathlib.Path(__file__).resolve().parents[1]
pack = root / "packs/vartovyi"
for profile in [None, *sorted((pack / "examples").glob("*.hcl"))]:
    with tempfile.TemporaryDirectory(prefix="vartovyi-pack-") as output:
        cmd = ["nomad-pack", "render", str(pack), "--to-dir", output, "--auto-approve"]
        if profile:
            cmd += ["--var-file", str(profile)]
        result = subprocess.run(cmd, capture_output=True, text=True)
        if result.returncode:
            raise SystemExit(result.stderr or result.stdout)
        files = list(pathlib.Path(output).rglob("*.nomad")) + list(pathlib.Path(output).rglob("*.nomad.hcl"))
        if not files:
            # CLI versions may preserve the template suffix when writing output.
            files = list(pathlib.Path(output).rglob("*.nomad.tpl"))
        if not files:
            raise SystemExit("nomad-pack did not produce any jobspecs")
        for path in files:
            subprocess.run(["nomad", "job", "validate", str(path)], check=True)
        print(f"Validated {profile.name if profile else 'defaults'}")
