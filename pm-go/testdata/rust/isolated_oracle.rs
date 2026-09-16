// Test-only adapter for complete project-local isolated link passes.
use aube_linker::{LinkStats, LinkStrategy, Linker};
use aube_lockfile::graph_hash::GraphHashes;
use aube_store::{PackageIndex, Store, StoredFile};
use serde_json::{Value, json};
use std::collections::BTreeMap;
use std::path::{Path, PathBuf};

pub fn run(path: &Path) {
    static PROFILE: aube_util::Embedder = aube_util::Embedder {
        name: "nub",
        ..aube_util::AUBE
    };
    aube_util::set_embedder(&PROFILE);
    let inputs: Vec<Value> = serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap();
    let outputs: Vec<_> = inputs.iter().map(link).collect();
    println!("{}", json!(outputs));
}

fn strings(value: &Value) -> Vec<String> {
    value
        .as_array()
        .unwrap()
        .iter()
        .map(|v| v.as_str().unwrap().into())
        .collect()
}

fn link(input: &Value) -> Value {
    let root = PathBuf::from(input["root"].as_str().unwrap());
    let graph = crate::peer_oracle::graph(&input["graph"]);
    let mut store = Store::with_dirs(
        root.parent().unwrap().join("oracle-cas"),
        root.parent().unwrap().join("oracle-cache"),
    );
    if input["global"].as_bool().unwrap() {
        store = store.with_virtual_store_dir(PathBuf::from(input["globalRoot"].as_str().unwrap()));
    }
    let mut indices = BTreeMap::new();
    for (key, files) in input["indices"].as_object().unwrap() {
        let mut index = PackageIndex::default();
        for (name, file) in files.as_object().unwrap() {
            index.insert(
                name.clone(),
                StoredFile {
                    hex_hash: file["hex_hash"].as_str().unwrap().into(),
                    store_path: PathBuf::from(file["store_path"].as_str().unwrap()),
                    executable: file["executable"].as_bool().unwrap(),
                    size: file["size"].as_u64(),
                },
            );
        }
        indices.insert(key.clone(), index);
    }
    let workspace: BTreeMap<_, _> = input["workspace"]
        .as_object()
        .unwrap()
        .iter()
        .map(|(k, v)| (k.clone(), PathBuf::from(v.as_str().unwrap())))
        .collect();
    let mut linker = Linker::new(&store, LinkStrategy::Copy)
        .with_use_global_virtual_store(input["global"].as_bool().unwrap())
        .with_modules_dir_name(input["modules"].as_str().unwrap())
        .with_aube_dir_override(PathBuf::from(input["virtual"].as_str().unwrap()))
        .with_hoist(input["hoist"].as_bool().unwrap())
        .with_hoist_workspace_packages(input["hoistWorkspace"].as_bool().unwrap())
        .with_shamefully_hoist(input["shamefully"].as_bool().unwrap())
        .with_dedupe_direct_deps(input["dedupe"].as_bool().unwrap())
        .with_virtual_store_only(input["only"].as_bool().unwrap())
        .with_public_hoist_pattern(&strings(&input["public"]));
    if input["patterns"].is_array() {
        linker = linker.with_hoist_pattern(&strings(&input["patterns"]));
    }
    if input["hashes"].is_object() {
        linker = linker.with_graph_hashes(GraphHashes {
            node_hash: input["hashes"]
                .as_object()
                .unwrap()
                .iter()
                .map(|(k, v)| (k.clone(), v.as_str().unwrap().to_string()))
                .collect(),
        });
    }
    if input["disk"].is_array() {
        linker = linker.with_disk_materialize(&strings(&input["disk"]));
    }
    if input["projectLocal"].is_object() {
        linker = linker.with_project_local_dep_paths(
            input["projectLocal"].as_object().unwrap().keys().cloned(),
        );
    }
    let mut passes = Vec::new();
    for _ in 0..2 {
        let result = if input["hasWorkspace"].as_bool().unwrap() {
            linker.link_workspace(&root, &graph, &indices, &workspace)
        } else {
            linker.link_all(&root, &graph, &indices)
        };
        let (stats, error) = match result {
            Ok(s) => (s, None),
            Err(e) => (LinkStats::default(), Some(e.to_string())),
        };
        let mut tree = BTreeMap::new();
        crate::materialize_oracle::snapshot(&root, Path::new(""), &mut tree);
        let mut global_tree = BTreeMap::new();
        let global_root = PathBuf::from(input["globalRoot"].as_str().unwrap());
        if global_root.exists() && !global_root.as_os_str().is_empty() {
            crate::materialize_oracle::snapshot(&global_root, Path::new(""), &mut global_tree);
        }
        passes.push(
            json!({"tree":tree,"globalTree":global_tree,"error":error,"stats":{
                "PackagesLinked":stats.packages_linked, "PackagesCached":stats.packages_cached,
                "FilesLinked":stats.files_linked, "TopLevelLinked":stats.top_level_linked,
            }}),
        );
    }
    json!(passes)
}
