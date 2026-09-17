#!/usr/bin/env python3
"""Snapshot engine setting metadata and generate calls to reference accessors.

Production consumes the checked-in snapshot only. The Rust output is test-only.
"""
import argparse
import json
from pathlib import Path
import re
try:
    import tomllib
except ImportError:
    from pip._vendor import tomli as tomllib

ROOT = Path(__file__).resolve().parents[2]
SOURCE = ROOT / "vendor/aube/crates/aube-settings/settings.toml"
DEST = ROOT / "pm-go/internal/settings/catalog.json"


def unsupported_settings():
    profile = (ROOT / "crates/nub-cli/src/pm_engine/identity.rs").read_text(encoding="utf-8")
    block = re.search(r"(?ms)^    unsupported_settings: &\[(.*?)^    \],", profile)[1]
    entries = {}
    for match in re.finditer(r'\(\s*"([^"]+)",\s*("(?:\\.|[^"\\])*")\s*,?\s*\)', block, re.DOTALL):
        entries[match[1]] = json.loads(re.sub(r"\\\n\s*", "", match[2]))
    assert len(entries) == 15, "audit changes to Nub's unsupported setting declarations"
    return entries, block


def kebab(name):
    return re.sub(r"([a-z0-9])([A-Z])", r"\1-\2", name).lower()


def camel(name):
    return re.sub(r"-(.)", lambda m: m[1].upper(), name)


def append_unique(out, values):
    for value in values:
        if value not in out:
            out.append(value)


def default_value(name, row, kind):
    raw = row["default"].strip()
    if name in {"preferFrozenLockfile", "storeDir", "cacheDir", "nodeVersion", "catalogPrune"}:
        return None
    if raw in {"undefined", "null"} or raw.startswith("null "):
        return None
    if kind == "bool":
        return {"true": True, "false": False}.get(raw)
    if kind == "int":
        return int(raw) if re.fullmatch(r"\+?[0-9]+", raw) and int(raw) < 2**64 else None
    if kind in {"list", "enum"} or kind == "string" and raw.startswith('"') and raw.endswith('"'):
        try:
            value = tomllib.loads("value = " + raw)["value"]
        except ValueError:
            return None
        if kind == "list":
            return value if isinstance(value, list) and all(isinstance(v, str) for v in value) else None
        return value if isinstance(value, str) else None
    if kind == "string":
        return None if raw.startswith("platform-") or raw == "auto-detected" or re.search(r"\s|`", raw) else raw
    return None


def catalog():
    result = []
    unsupported, _ = unsupported_settings()
    for name, row in sorted(tomllib.loads(SOURCE.read_text(encoding="utf-8")).items()):
        sources = row.get("sources", {})
        env = []
        for alias in sources.get("env", []):
            env.append(alias)
            for prefix, replacement in [("npm_config_", "pnpm_config_"), ("NPM_CONFIG_", "PNPM_CONFIG_")]:
                if alias.startswith(prefix):
                    append_unique(env, [replacement + alias[len(prefix):]])
        npmrc = list(dict.fromkeys(sources.get("npmrc", [])))
        for key in list(npmrc):
            if not key.startswith(("/", "@")) and ":" not in key:
                append_unique(npmrc, [kebab(key), camel(key)])
        order = ["cli", "env", "projectConfig"]
        expansions = {"npmrc": ["projectNpmrc", "userNpmrc"], "aubeConfig": ["projectAubeConfig", "userAubeConfig"]}
        for source in row.get("precedence", []):
            append_unique(order, expansions.get(source, [source]))
        append_unique(order, ["workspaceYaml", "globalConfigYaml", "projectAubeConfig", "projectNpmrc", "userAubeConfig", "userNpmrc", "embedderDefaults"])
        ty = row["type"]
        kind = {"bool": "bool", "int": "int", "list<string>": "list", "path": "string", "url": "string", "string": "string"}.get(ty, "unsupported")
        variants = []
        if ty.startswith('"'):
            kind = "string"
            pieces = [p.strip() for p in ty.split("|")]
            if all(re.fullmatch(r'"[a-z][a-z0-9-]*"', p) for p in pieces):
                kind, variants = "enum", [p[1:-1] for p in pieces]
        result.append(dict(name=name, type=ty, kind=kind, default=default_value(name, row, kind),
                           defaultText=row["default"], cli=sources.get("cli", []), env=env, npmrc=npmrc,
                           yaml=sources.get("workspaceYaml", []), precedence=order, variants=variants,
                           layout=row.get("layout", False), npmShared=row.get("npmShared", False),
                           managed=row.get("managedPolicy", ""), explicit=row.get("explicitAccessor", False),
                           unsupportedAdvice=unsupported.get(name, "")))
    assert set(unsupported).issubset({row["name"] for row in result})
    return result


def oracle(rows):
    _, unsupported = unsupported_settings()
    lines = ["// Generated test-only calls to the baseline's public typed accessors.",
             "const NUB_UNSUPPORTED_SETTINGS: &[(&str, &str)] = &[" + unsupported + "];",
             "fn resolved(name: &str, explicit: bool, ctx: &aube_settings::ResolveCtx<'_>) -> serde_json::Value {", "match name {"]
    for row in rows:
        if row["kind"] == "unsupported":
            continue
        fn = kebab(row["name"]).replace("-", "_").replace(".", "_")
        call = f"aube_settings::resolved::{fn}(ctx)"
        if row["kind"] == "enum":
            call += ".as_str()" if row["default"] is not None else ".map(|v| v.as_str())"
        if row["explicit"]:
            lines.append(f'{json.dumps(row["name"])} if explicit => serde_json::json!(aube_settings::resolved::{fn}_explicit(ctx)),')
        lines.append(f'{json.dumps(row["name"])} => serde_json::json!({call}),')
    lines.extend(["_ => serde_json::Value::Null,", "}", "}"])
    return "\n".join(lines) + "\n"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true")
    parser.add_argument("--oracle", type=Path)
    args = parser.parse_args()
    rows = catalog()
    text = json.dumps(rows, ensure_ascii=False, indent=2) + "\n"
    if args.oracle:
        args.oracle.write_text(oracle(rows), encoding="utf-8")
    elif args.check:
        if DEST.read_text(encoding="utf-8") != text:
            raise SystemExit("Setting metadata changed; regenerate with python3 pm-go/scripts/extract_settings.py")
    else:
        DEST.parent.mkdir(parents=True, exist_ok=True)
        with DEST.open("w", encoding="utf-8", newline="\n") as out:
            out.write(text)


if __name__ == "__main__":
    main()
