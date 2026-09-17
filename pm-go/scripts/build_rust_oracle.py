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
required = {"aube", "aube_lockfile", "aube_manifest", "aube_util", "aube_resolver", "aube_registry", "aube_store", "aube_linker", "aube_settings", "yaml_serde", "anyhow", "tokio", "serde_json", "node_semver", "serde", "rayon", "blake3", "hex", "miette"}
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
for name in ["verify_install_layout", "gvs_nested_links_are_current", "stale_gvs_nested_link", "read_installed_package_manifest", "hash_file_if_exists", "empty_blake3_hash", "package_jsons_stale", "deferred_dep_builds_stale", "preview_list", "hash_file", "license_state_fingerprint"]:
    function = re.search(r"(?ms)^(?:pub )?fn " + name + r"\(.*?^\}", state_source).group()
    if name == "package_jsons_stale":
        # Cargo builds two serde_json crate identities. Infer the engine's
        # Value through FromStr, parsing the original bytes exactly once;
        # serializing/reparsing our Value would change float rounding.
        old = "let parsed: Result<serde_json::Value, _> = serde_json::from_slice(&content);\n        let Ok(parsed) = parsed else {"
        assert function.count(old) == 1
        function = function.replace(old, "let parsed = std::str::from_utf8(&content).ok().and_then(|s| s.parse().ok());\n        let Some(parsed) = parsed else {")
    state_probe += "\n" + function
state_path = output.parent / "state-reference.rs"
state_path.write_text(state_probe)
delta_source = (root / "vendor/aube/crates/aube/src/commands/install/delta.rs").read_text()
delta_path = output.parent / "delta-reference.rs"
delta_path.write_text(delta_source[delta_source.index("use aube_lockfile::"):delta_source.index("#[cfg(test)]")])
gvs_source = (root / "vendor/aube/crates/aube/src/commands/install/gvs.rs").read_text()
settings_source = (root / "vendor/aube/crates/aube/src/commands/install/settings.rs").read_text()
gvs_probe = "use miette::miette;\n"
start = gvs_source.index("#[derive(Clone, Copy, Debug, PartialEq, Eq)]\npub(super) enum Materialization")
gvs_probe += gvs_source[start:gvs_source.index("/// Reject the contradictory explicit request", start)]
for source, names in [(gvs_source, ["planned_global_virtual_store", "reject_gvs_layout_contradiction", "prewarm_global_virtual_store_override"]), (settings_source, ["find_gvs_incompatible_trigger", "detect_aube_dir_gvs_mode"])]:
    for name in names:
        gvs_probe += "\n" + re.search(r"(?ms)^pub\(super\) fn " + name + r"(?:<.*?>)?\(.*?^\}", source).group()
gvs_path = output.parent / "gvs-reference.rs"
gvs_path.write_text(gvs_probe)
settings_path = output.parent / "settings-reference.rs"
subprocess.run([sys.executable, str(root / "pm-go/scripts/extract_settings.py"), "--oracle", str(settings_path)], check=True)
native_source = (root / "crates/nub-cli/src/project_config.rs").read_text()
adapter_source = (root / "crates/nub-cli/src/pm_engine/mod.rs").read_text()
native_probe = "use serde_json::Value;\nuse std::{path::PathBuf, time::Duration};\ntype Result<T> = std::result::Result<T, ConfigError>;\n"
for name, kind, derive in [("ConfigError", "enum", "Debug"), ("InstallConfig", "struct", "Debug, Default, Clone, PartialEq"), ("LinkerConfig", "enum", "Debug, Clone, PartialEq"), ("Hoist", "enum", "Debug, Clone, PartialEq")]:
    native_probe += f"\n#[derive({derive})]\n" + re.search(r"(?ms)^pub " + kind + " " + name + r" \{.*?^\}", native_source).group()
native_probe += "\npub type PublicHoist = Vec<String>;\n"
for name in ["INSTALL_KEYS", "LINKER_STRATEGY_KEYS"]:
    native_probe += re.search(r"(?ms)^(?:pub\(crate\) )?const " + name + r":.*?^\];", native_source).group() + "\n"
for name in ["child", "named_path", "as_object", "as_str", "as_string_array", "reject_unknown_keys", "validate_linker", "validate_hoist", "linker_without_options", "unknown_strategy", "validate_install", "parse_duration"]:
    native_probe += "\n" + re.search(r"(?ms)^(?:pub\(crate\) )?fn " + name + r"(?:<[^\n]*>)?\(.*?^\}", native_source).group()
native_probe += "\npub fn parse(v: &Value) -> Result<InstallConfig> { validate_install(v, \"install\") }\n"
native_path = output.parent / "native-install-reference.rs"
native_path.write_text(native_probe)
lower_probe = "use anyhow::Result;\n#[derive(Debug, Default, PartialEq, Eq)]\n"
lower_probe += re.search(r"(?ms)^struct NativeInstallSettings \{.*?^\}", adapter_source).group()
lower_probe += "\n#[derive(Clone, Copy, PartialEq, Eq)]\n" + re.search(r"(?ms)^enum VirtualStoreLocality \{.*?^\}", adapter_source).group()
for name in ["lower_native_install_settings", "scoped_install_settings"]:
    lower_probe += "\n" + re.search(r"(?ms)^fn " + name + r"\(.*?^\}", adapter_source).group()
lower_path = output.parent / "native-lower-reference.rs"
lower_path.write_text(lower_probe)
command = ["rustc", "--edition=2024", str(root / "pm-go/testdata/rust/lockfile_oracle.rs"),
           "-o", str(output)]
for name, path in sorted(artifacts.items()):
    command.extend(["--extern", f"{name}={path}", "-L", f"dependency={Path(path).parent}"])
subprocess.run(command, cwd=root, check=True, env={**os.environ, "PM_STATE_ORACLE_SOURCE": str(state_path), "PM_DELTA_ORACLE_SOURCE": str(delta_path), "PM_GVS_ORACLE_SOURCE": str(gvs_path), "PM_SETTINGS_ORACLE_SOURCE": str(settings_path), "PM_NATIVE_INSTALL_ORACLE_SOURCE": str(native_path), "PM_NATIVE_LOWER_ORACLE_SOURCE": str(lower_path)})
