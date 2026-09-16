#!/usr/bin/env python3
"""Build the existing reference and a test-only probe from its exact artifacts."""

import json
from pathlib import Path
import subprocess
import sys

root = Path(__file__).resolve().parents[2]
artifacts = {}
with subprocess.Popen(
    ["cargo", "build", "--locked", "-p", "nub-cli", "--profile", "fast",
     "--message-format=json-render-diagnostics"],
    cwd=root, stdout=subprocess.PIPE, text=True,
) as process:
    for line in process.stdout:
        event = json.loads(line)
        if event.get("reason") != "compiler-artifact":
            continue
        name = event["target"]["name"]
        if name in {"aube_lockfile", "aube_manifest", "aube_util", "aube_resolver", "aube_registry", "aube_store", "serde_json", "node_semver"}:
            libs = [p for p in event["filenames"] if p.endswith(".rlib")]
            if len(libs) != 1:
                raise RuntimeError(f"Expected one reference library for {name}: {libs}")
            artifacts[name] = libs[0]
    if process.wait() != 0:
        sys.exit(process.returncode)

if set(artifacts) != {"aube_lockfile", "aube_manifest", "aube_util", "aube_resolver", "aube_registry", "aube_store", "serde_json", "node_semver"}:
    raise RuntimeError(f"Missing reference artifacts: {artifacts}")
output = root / "target/pm-go/rust-lockfile-oracle"
output.parent.mkdir(parents=True, exist_ok=True)
command = ["rustc", "--edition=2024", str(root / "pm-go/testdata/rust/lockfile_oracle.rs"),
           "-o", str(output)]
for name, path in sorted(artifacts.items()):
    command.extend(["--extern", f"{name}={path}", "-L", f"dependency={Path(path).parent}"])
subprocess.run(command, cwd=root, check=True)
