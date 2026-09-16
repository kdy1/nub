// Test-only graph inputs for peer passes; calls the unchanged resolver library.
use aube_lockfile::{DepType, DirectDep, LockedPackage, LockfileGraph, PeerDepMeta};
use serde_json::{Value, json};
use std::collections::BTreeMap;

fn strings(v: &Value) -> BTreeMap<String, String> {
    v.as_object()
        .map(|m| {
            m.iter()
                .map(|(k, v)| (k.clone(), v.as_str().unwrap().into()))
                .collect()
        })
        .unwrap_or_default()
}
fn graph(v: &Value) -> LockfileGraph {
    let mut g = LockfileGraph::default();
    for (path, deps) in v["Importers"].as_object().unwrap() {
        g.importers.insert(
            path.clone(),
            deps.as_array()
                .unwrap()
                .iter()
                .map(|d| DirectDep {
                    name: d["Name"].as_str().unwrap().into(),
                    dep_path: d["DepPath"].as_str().unwrap().into(),
                    dep_type: match d["Type"].as_u64().unwrap() {
                        0 => DepType::Production,
                        1 => DepType::Dev,
                        2 => DepType::Optional,
                        _ => panic!("dep type"),
                    },
                    specifier: d["Specifier"].as_str().map(String::from),
                })
                .collect(),
        );
    }
    for (key, p) in v["Packages"].as_object().unwrap() {
        let mut pkg = LockedPackage {
            name: p["Name"].as_str().unwrap().into(),
            version: p["Version"].as_str().unwrap().into(),
            dep_path: p["DepPath"].as_str().unwrap().into(),
            dependencies: strings(&p["Dependencies"]),
            optional_dependencies: strings(&p["OptionalDependencies"]),
            peer_dependencies: strings(&p["PeerDependencies"]),
            ..Default::default()
        };
        if let Some(meta) = p["PeerDependenciesMeta"].as_object() {
            for (name, m) in meta {
                pkg.peer_dependencies_meta.insert(
                    name.clone(),
                    PeerDepMeta {
                        optional: m["Optional"].as_bool().unwrap(),
                    },
                );
            }
        }
        g.packages.insert(key.clone(), pkg);
    }
    g
}
pub fn run(path: &std::path::Path) {
    let inputs: Value = serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap();
    let outputs: Vec<_> = inputs.as_array().unwrap().iter().map(|input| {
        let g = graph(input);
        let unmet: Vec<_> = aube_resolver::detect_unmet_peers(&g).into_iter().map(|p| json!({"FromDepPath":p.from_dep_path,"FromName":p.from_name,"PeerName":p.peer_name,"Declared":p.declared,"Found":p.found})).collect();
        let (mut g, hoisted) = aube_resolver::hoist_auto_installed_peers(g);
        let snapshot = crate::graph_snapshot::snapshot(&g);
        let names: BTreeMap<_,Vec<_>> = hoisted.iter().map(|(path,names)| (path,names.iter().collect())).collect();
        aube_resolver::remove_auto_installed_peers(&mut g,&hoisted);
        json!({"Hoisted":names,"Graph":snapshot,"Removed":crate::graph_snapshot::snapshot(&g),"Unmet":unmet})
    }).collect();
    println!("{}", json!(outputs));
}
