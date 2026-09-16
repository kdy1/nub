// Test-only graph inputs for peer passes; calls the unchanged resolver library.
use aube_lockfile::{DepType, DirectDep, LocalSource, LockedPackage, LockfileGraph, PeerDepMeta};
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
        if p["Source"].is_object() {
            let s = &p["Source"];
            let path = std::path::PathBuf::from(s["Path"].as_str().unwrap());
            pkg.local_source = Some(match s["Kind"].as_u64().unwrap() {
                0 => LocalSource::Directory(path),
                1 => LocalSource::Tarball(path),
                2 => LocalSource::Link(path),
                3 => LocalSource::Portal(path),
                4 => LocalSource::Exec(path),
                5 => LocalSource::Git(aube_lockfile::GitSource {
                    url: s["URL"].as_str().unwrap().into(),
                    resolved: s["Resolved"].as_str().unwrap().into(),
                    committish: s["Committish"].as_str().map(String::from),
                    integrity: s["Integrity"].as_str().map(String::from),
                    subpath: s["Subpath"].as_str().map(String::from),
                }),
                6 => LocalSource::RemoteTarball(aube_lockfile::RemoteTarballSource {
                    url: s["URL"].as_str().unwrap().into(),
                    integrity: s["Integrity"].as_str().unwrap_or("").into(),
                    git_hosted: s["GitHosted"].as_bool().unwrap(),
                }),
                _ => panic!("peer fixture source"),
            });
        }
        g.packages.insert(key.clone(), pkg);
    }
    g
}

pub fn contexts(path: &std::path::Path) {
    let inputs: Value = serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap();
    let outputs: Vec<_> = inputs
        .as_array()
        .unwrap()
        .iter()
        .map(|input| {
            let mut g = graph(&input["Graph"]);
            let o = &input["Options"];
            let options = aube_resolver::PeerContextOptions {
                dedupe_peer_dependents: o["DedupePeerDependents"].as_bool().unwrap(),
                dedupe_peers: o["DedupePeers"].as_bool().unwrap(),
                resolve_from_workspace_root: o["ResolveFromWorkspaceRoot"].as_bool().unwrap(),
                peers_suffix_max_length: o["PeersSuffixMaxLength"].as_u64().unwrap() as usize,
            };
            let mut hoisted = aube_resolver::AutoInstalledPeers::new();
            if input["Hoist"].as_bool().unwrap() {
                (g, hoisted) = aube_resolver::hoist_auto_installed_peers(g);
            }
            match aube_resolver::apply_peer_contexts(g, &options) {
                Ok(mut out) => {
                    aube_resolver::remove_auto_installed_peers(&mut out, &hoisted);
                    json!({"Graph":crate::graph_snapshot::snapshot(&out),"Error":null})
                }
                Err(e) => json!({"Graph":null,"Error":e.to_string()}),
            }
        })
        .collect();
    println!("{}", json!(outputs));
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
