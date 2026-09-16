// Test-only adapter to the unchanged Rust lockfile library. This is not linked
// into, shipped with, or invoked by the Go package manager.
mod drift_oracle;
mod git_oracle;
mod graph_snapshot;
mod override_oracle;
mod peer_oracle;
mod semver_oracle;
mod trust_oracle;

fn main() {
    let args: Vec<_> = std::env::args_os().skip(1).collect();
    if args.len() == 2 && args[0] == "git-refs" {
        git_oracle::refs(std::path::Path::new(&args[1]));
        return;
    }
    if args.len() == 2 && args[0] == "exec-path" {
        peer_oracle::exec_paths(std::path::Path::new(&args[1]));
        return;
    }
    if args.len() == 2 && args[0] == "version-policy" {
        trust_oracle::policy(std::path::Path::new(&args[1]));
        return;
    }
    if args.len() == 2 && args[0] == "trust" {
        trust_oracle::trust(std::path::Path::new(&args[1]));
        return;
    }
    if args.len() == 2 && args[0] == "peer-contexts" {
        peer_oracle::contexts(std::path::Path::new(&args[1]));
        return;
    }
    if args.len() == 2 && args[0] == "peer-hoist" {
        peer_oracle::run(std::path::Path::new(&args[1]));
        return;
    }
    if args.len() == 2 && args[0] == "overrides" {
        override_oracle::run(std::path::Path::new(&args[1]));
        return;
    }
    if args.len() == 2 && args[0] == "semver" {
        semver_oracle::run(std::path::Path::new(&args[1]));
        return;
    }
    if args.len() == 2 && (args[0] == "noop-write" || args[0] == "read-project") {
        static PROFILE: aube_util::Embedder = aube_util::Embedder {
            no_churn_lockfile_write: true,
            strict_unsupported_source: true,
            lockfile_basename: "nub.lock",
            lockfile_legacy_basenames: &["lock.yaml"],
            canonical_lockfile_always_wins: false,
            self_names: &["nub"],
            ..aube_util::AUBE
        };
        aube_util::set_embedder(&PROFILE);
        let dir = std::path::Path::new(&args[1]);
        let manifest = aube_manifest::PackageJson::from_path(&dir.join("package.json")).unwrap();
        if args[0] == "read-project" {
            let result = aube_lockfile::parse_lockfile_with_kind(dir, &manifest);
            println!(
                "{}",
                match result {
                    Ok((graph, _)) =>
                        serde_json::json!({"ok":true,"graph":graph_snapshot::snapshot(&graph)}),
                    Err(e) => serde_json::json!({"ok":false,"error":e.to_string()}),
                }
            );
            return;
        }
        let result =
            aube_lockfile::parse_lockfile_with_kind(dir, &manifest).and_then(|(graph, kind)| {
                aube_lockfile::write_lockfile_as(dir, &graph, &manifest, kind)
            });
        println!(
            "{}",
            match result {
                Ok(_) => serde_json::json!({"ok":true}),
                Err(e) => serde_json::json!({"ok":false,"error":e.to_string()}),
            }
        );
        return;
    }
    if args.len() == 3 && args[0] == "importer-drift" {
        drift_oracle::importer(
            std::path::Path::new(&args[1]),
            std::path::Path::new(&args[2]),
        );
        return;
    }
    if (args.len() == 4 && args[0] == "yarn-graph")
        || (args.len() == 5 && (args[0] == "yarn-classic-write" || args[0] == "yarn-berry-write"))
    {
        if args.last().unwrap() == "strict" {
            static STRICT: aube_util::Embedder = aube_util::Embedder {
                strict_unsupported_source: true,
                ..aube_util::AUBE
            };
            aube_util::set_embedder(&STRICT);
        }
        let manifest = aube_manifest::PackageJson::from_path(std::path::Path::new(&args[2]))
            .expect("reference manifest");
        let result = aube_lockfile::yarn::parse(std::path::Path::new(&args[1]), &manifest);
        let result = match result {
            Ok(graph) if args[0] == "yarn-berry-write" => {
                match aube_lockfile::yarn::write_berry(
                    std::path::Path::new(&args[3]),
                    &graph,
                    &manifest,
                ) {
                    Ok(()) => serde_json::json!({"ok": true}),
                    Err(error) => serde_json::json!({"ok": false, "error": error.to_string()}),
                }
            }
            Ok(graph) if args[0] == "yarn-classic-write" => {
                match aube_lockfile::yarn::write_classic(
                    std::path::Path::new(&args[3]),
                    &graph,
                    &manifest,
                ) {
                    Ok(()) => serde_json::json!({"ok": true}),
                    Err(error) => serde_json::json!({"ok": false, "error": error.to_string()}),
                }
            }
            Ok(graph) => serde_json::json!({"ok": true, "graph": graph_snapshot::snapshot(&graph)}),
            Err(error) => serde_json::json!({"ok": false, "error": error.to_string()}),
        };
        println!("{result}");
        return;
    }
    if (args.len() == 3 && args[0] == "bun-graph") || (args.len() == 5 && args[0] == "bun-write") {
        if args.last().unwrap() == "strict" {
            static STRICT: aube_util::Embedder = aube_util::Embedder {
                strict_unsupported_source: true,
                ..aube_util::AUBE
            };
            aube_util::set_embedder(&STRICT);
        }
        let result = aube_lockfile::bun::parse(std::path::Path::new(&args[1]));
        let result = match result {
            Ok(graph) if args[0] == "bun-write" => {
                let manifest =
                    aube_manifest::PackageJson::from_path(std::path::Path::new(&args[2]))
                        .expect("reference manifest");
                match aube_lockfile::bun::write(std::path::Path::new(&args[3]), &graph, &manifest) {
                    Ok(()) => serde_json::json!({"ok": true}),
                    Err(error) => serde_json::json!({"ok": false, "error": error.to_string()}),
                }
            }
            Ok(graph) => serde_json::json!({"ok": true, "graph": graph_snapshot::snapshot(&graph)}),
            Err(error) => serde_json::json!({"ok": false, "error": error.to_string()}),
        };
        println!("{result}");
        return;
    }
    if args.len() == 5 && args[0] == "pnpm-write" {
        let options = aube_lockfile::ParseOptions {
            strict_store_integrity: args[4] != "relaxed",
        };
        let result =
            aube_lockfile::pnpm::parse_with_options(std::path::Path::new(&args[1]), options);
        let result = result.and_then(|graph| {
            let manifest = aube_manifest::PackageJson::from_path(std::path::Path::new(&args[2]))
                .expect("reference manifest");
            aube_lockfile::pnpm::write(std::path::Path::new(&args[3]), &graph, &manifest)
        });
        let result = match result {
            Ok(()) => serde_json::json!({"ok": true}),
            Err(error) => serde_json::json!({"ok": false, "error": error.to_string()}),
        };
        println!("{result}");
        return;
    }
    if args.len() == 3 && args[0] == "pnpm-graph" {
        let options = aube_lockfile::ParseOptions {
            strict_store_integrity: args[2] != "relaxed",
        };
        let result =
            aube_lockfile::pnpm::parse_with_options(std::path::Path::new(&args[1]), options);
        let result = match result {
            Ok(graph) => serde_json::json!({"ok": true, "graph": graph_snapshot::snapshot(&graph)}),
            Err(error) => serde_json::json!({"ok": false, "error": error.to_string()}),
        };
        println!("{result}");
        return;
    }
    assert_eq!(args.len(), 2, "expected lockfile and manifest paths");
    let path = std::path::Path::new(&args[0]);
    let manifest = aube_manifest::PackageJson::from_path(std::path::Path::new(&args[1]))
        .expect("read reference manifest");
    let graph = aube_lockfile::npm::parse(path, &manifest).expect("read reference lockfile");
    aube_lockfile::npm::write(path, &graph, &manifest).expect("write reference lockfile");
}
