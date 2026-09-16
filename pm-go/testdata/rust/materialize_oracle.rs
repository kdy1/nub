use aube_lockfile::{LockedPackage, LockfileGraph, graph_hash::GraphHashes};
use aube_store::{PackageIndex, Store, StoredFile};
use serde_json::{Value, json};
use std::collections::BTreeMap;
use std::path::{Path, PathBuf};

pub fn run(path: &Path) {
    let cases: Vec<Value> = serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap();
    let results: Vec<_> = cases.iter().map(materialize).collect();
    println!("{}", serde_json::to_string(&results).unwrap());
}

fn strings(value: &Value) -> BTreeMap<String, String> {
    value
        .as_object()
        .map(|m| {
            m.iter()
                .map(|(k, v)| (k.clone(), v.as_str().unwrap().to_string()))
                .collect()
        })
        .unwrap_or_default()
}

fn materialize(case: &Value) -> Value {
    let root = PathBuf::from(case["root"].as_str().unwrap());
    let store = Store::with_dirs(
        root.parent().unwrap().join("oracle-cas"),
        root.parent().unwrap().join("oracle-cache"),
    )
    .with_virtual_store_dir(root.clone());
    let linker = aube_linker::Linker::new(&store, aube_linker::LinkStrategy::Copy)
        .with_virtual_store_dir_max_length(case["limit"].as_u64().unwrap() as usize)
        .with_graph_hashes(GraphHashes {
            node_hash: strings(&case["hashes"]),
        });
    let mut graph = LockfileGraph::default();
    for (key, value) in case["packages"].as_object().unwrap() {
        graph.packages.insert(
            key.clone(),
            LockedPackage {
                name: value["Name"].as_str().unwrap().into(),
                version: value["Version"].as_str().unwrap().into(),
                dep_path: value["DepPath"].as_str().unwrap().into(),
                dependencies: strings(&value["Dependencies"]),
                ..Default::default()
            },
        );
    }
    let nested: BTreeMap<_, _> = strings(&case["nested"])
        .into_iter()
        .map(|(k, v)| (k, PathBuf::from(v)))
        .collect();
    let mut indices = BTreeMap::new();
    for (key, value) in case["indices"].as_object().unwrap() {
        let mut index = PackageIndex::new();
        for (name, file) in value.as_object().unwrap() {
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
    let mut passes = Vec::new();
    for _ in 0..2 {
        let mut stats = Vec::new();
        for key in case["order"].as_array().unwrap() {
            let key = key.as_str().unwrap();
            let mut counts = aube_linker::LinkStats::default();
            linker
                .ensure_in_virtual_store(
                    key,
                    &graph,
                    &graph.packages[key],
                    &indices[key],
                    &mut counts,
                    Some(&nested),
                )
                .unwrap();
            stats.push(json!({"cached": counts.packages_cached > 0, "files":counts.files_linked}));
        }
        let mut tree = BTreeMap::new();
        snapshot(&root, Path::new(""), &mut tree);
        passes.push(json!({"stats":stats, "tree":tree}));
    }
    json!({"passes":passes})
}

fn snapshot(root: &Path, relative: &Path, tree: &mut BTreeMap<String, Value>) {
    for child in std::fs::read_dir(root.join(relative)).unwrap() {
        let child = child.unwrap();
        let relative = relative.join(child.file_name());
        let path = root.join(&relative);
        let info = std::fs::symlink_metadata(&path).unwrap();
        let mut entry = serde_json::Map::new();
        if let Ok(target) = std::fs::read_link(&path) {
            entry.insert("link".into(), json!(target.to_string_lossy()));
        } else if info.is_dir() {
            entry.insert("directory".into(), json!(true));
            snapshot(root, &relative, tree);
        } else {
            entry.insert("body".into(), json!(std::fs::read_to_string(path).unwrap()));
        }
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            entry.insert("mode".into(), json!(info.permissions().mode() & 0o777));
        }
        tree.insert(
            relative.to_string_lossy().replace('\\', "/"),
            Value::Object(entry),
        );
    }
}
