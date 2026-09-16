#!/usr/bin/env python3
"""Build the existing reference and a test-only probe from its exact artifacts."""

import json
import os
from pathlib import Path
import re
import subprocess
import sys

root = Path(__file__).resolve().parents[2]
artifacts = {}
required = {"aube_lockfile", "aube_manifest", "aube_util", "aube_resolver", "aube_registry", "aube_store", "aube_linker", "tokio", "serde_json", "node_semver", "serde", "rayon", "blake3"}
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
        if name in required:
            libs = [p for p in event["filenames"] if p.endswith(".rlib")]
            if len(libs) != 1:
                raise RuntimeError(f"Expected one reference library for {name}: {libs}")
            artifacts[name] = libs[0]
    if process.wait() != 0:
        sys.exit(process.returncode)

if set(artifacts) != required:
    raise RuntimeError(f"Missing reference artifacts: {artifacts}")
output = root / "target/pm-go/rust-lockfile-oracle"
output.parent.mkdir(parents=True, exist_ok=True)
# State lives in a private engine module. Compile its unchanged declarations and
# pure filesystem checks into the test-only probe; never edit the Rust module or
# reproduce its logic in a separately maintained oracle implementation.
state_source = (root / "vendor/aube/crates/aube/src/state.rs").read_text()
start = state_source.index("#[derive(Debug, Serialize, Deserialize)]\npub struct InstallState {")
end = state_source.index("/// Check if install is needed.", start)
state_probe = "use rayon::prelude::*;\nuse serde::{Deserialize, Serialize};\nuse std::{collections::BTreeMap, path::Path};\n"
state_probe += state_source[start:end]
state_probe += "\n#[derive(Deserialize)]\n" + re.search(r"(?ms)^struct InstalledManifest \{.*?^\}", state_source).group()
for name in ["verify_install_layout", "gvs_nested_links_are_current", "stale_gvs_nested_link", "read_installed_package_manifest", "hash_file_if_exists", "empty_blake3_hash"]:
    state_probe += "\n" + re.search(r"(?ms)^(?:pub )?fn " + name + r"\(.*?^\}", state_source).group()
state_path = output.parent / "state-reference.rs"
state_path.write_text(state_probe)
command = ["rustc", "--edition=2024", str(root / "pm-go/testdata/rust/lockfile_oracle.rs"),
           "-o", str(output)]
for name, path in sorted(artifacts.items()):
    command.extend(["--extern", f"{name}={path}", "-L", f"dependency={Path(path).parent}"])
subprocess.run(command, cwd=root, check=True, env={**os.environ, "PM_STATE_ORACLE_SOURCE": str(state_path)})
