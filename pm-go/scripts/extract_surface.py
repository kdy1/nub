#!/usr/bin/env python3
"""Snapshot PM argument declarations; this is not an acceptance parser.

Nub's adapter may reject, consume, or transform engine arguments. Keep source
attributes and flatten edges intact so implementation work can audit those
decisions against the corresponding dispatch and differential tests.
"""

import argparse
import json
from pathlib import Path
import re

ROOT = Path(__file__).resolve().parents[2]
DEST = ROOT / "pm-go/internal/surface/arguments.json"


def definition(text, pattern):
    match = re.search(pattern, text, re.MULTILINE)
    if not match:
        raise ValueError(f"declaration not found: {pattern}")
    indent = match.group("indent")
    end = re.search(r"^" + re.escape(indent) + r"}[,;]?\s*$", text[match.end():], re.MULTILINE)
    if not end:
        raise ValueError(f"unterminated declaration: {pattern}")
    return text[match.end():match.end() + end.start()]


def fields(body):
    result, attrs, pending = [], [], []
    for line in body.splitlines():
        line = line.strip()
        if not line or line.startswith("//"):
            continue
        if pending or line.startswith("#["):
            pending.append(line)
            if line.endswith("]"):
                attrs.append(" ".join(pending))
                pending = []
            continue
        match = re.fullmatch(r"(?:pub(?:\([^)]*\))?\s+)?(\w+):\s*(.+),", line)
        if not match:
            raise ValueError(f"unrecognized argument field: {line}")
        result.append({"field": match[1], "rust_type": match[2], "attributes": attrs})
        attrs = []
    if attrs or pending:
        raise ValueError("dangling argument attributes")
    return result


def inventory():
    commands = json.loads((ROOT / "pm-go/internal/surface/commands.json").read_text(encoding="utf-8"))
    definitions = {}
    for tree in ["vendor/aube/crates/aube/src", "crates/nub-cli/src/pm_engine"]:
        for path in sorted((ROOT / tree).rglob("*.rs")):
            text = path.read_text(encoding="utf-8")
            for match in re.finditer(r"^pub struct (\w+)\s*\{", text, re.MULTILINE):
                definitions.setdefault(match[1], []).append((path, text))
    declarations, bindings = {}, {}

    def add(name, preferred=None):
        candidates = definitions.get(name, [])
        if preferred:
            local = [(p, t) for p, t in candidates if p.relative_to(ROOT).as_posix().startswith(preferred)]
            if local:
                candidates = local
        if len(candidates) != 1:
            raise ValueError(f"ambiguous or missing declaration {name}: {[str(p) for p, _ in candidates]}")
        path, text = candidates[0]
        key = f"{path.relative_to(ROOT).as_posix()}::{name}"
        if key in declarations:
            return key
        body = definition(text, r"^(?P<indent>)pub struct " + name + r"\s*\{")
        record = {"fields": fields(body)}
        declarations[key] = record
        for field in record["fields"]:
            if any(re.search(r"\bflatten\b", a) for a in field["attributes"]):
                nested = field["rust_type"].split("::")[-1]
                field["flatten"] = add(nested, path.parent.relative_to(ROOT).as_posix())
        return key

    cli = (ROOT / "crates/nub-cli/src/cli.rs").read_text(encoding="utf-8")
    for command in commands:
        rust = command["rust_args"]
        if rust in {"cli::Install", "cli::Ci"}:
            name = rust.split("::")[-1]
            key = "crates/nub-cli/src/cli.rs::" + name
            body = definition(cli, r"^(?P<indent>    )" + name + r"\s*\{")
            record = {"fields": fields(body)}
            declarations[key] = record
            for field in record["fields"]:
                if any(re.search(r"\bflatten\b", a) for a in field["attributes"]):
                    field["flatten"] = add(field["rust_type"].split("::")[-1], "crates/nub-cli/src/pm_engine/")
            bindings[command["canonical"]] = {"declaration": key}
        elif rust == "cli::run_pm":
            bindings[command["canonical"]] = {"manual_dispatch": "crates/nub-cli/src/cli.rs::run_pm"}
        else:
            _, module, name = rust.split("::")
            bindings[command["canonical"]] = {"declaration": add(name, "vendor/aube/crates/aube/src/commands/" + module)}
    return {"baseline": "2a4573ef059798b75f789aa8c22da51e470e3138",
            "scope": "Source declarations, including flattened structs. Adapter rewrites and manual subcommand grammars require separate verification.",
            "commands": bindings, "declarations": dict(sorted(declarations.items()))}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    text = json.dumps(inventory(), indent=2, ensure_ascii=False) + "\n"
    if args.check:
        if DEST.read_text(encoding="utf-8") != text:
            raise SystemExit("PM argument declarations changed; regenerate with python3 pm-go/scripts/extract_surface.py")
    else:
        DEST.write_text(text, encoding="utf-8", newline="\n")


if __name__ == "__main__":
    main()
