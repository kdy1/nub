// Test-only adapter to the unchanged Rust lockfile library. This is not linked
// into, shipped with, or invoked by the Go package manager.
mod graph_snapshot;

fn main() {
    let args: Vec<_> = std::env::args_os().skip(1).collect();
    if (args.len() == 4 && args[0] == "yarn-graph")
        || (args.len() == 5 && args[0] == "yarn-classic-write")
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
