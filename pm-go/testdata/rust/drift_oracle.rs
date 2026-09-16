use aube_lockfile::{
    DepType, DirectDep, DriftStatus, LocalSource, LockedPackage, LockfileGraph, LockfileKind,
};
use std::collections::BTreeMap;
use std::path::Path;

pub fn importer(request: &Path, manifest_path: &Path) {
    let input: serde_json::Value =
        serde_json::from_slice(&std::fs::read(request).unwrap()).unwrap();
    let manifest = aube_manifest::PackageJson::from_path(manifest_path).unwrap();
    let strings = |v: &serde_json::Value| -> BTreeMap<String, String> {
        v.as_object()
            .into_iter()
            .flatten()
            .map(|(k, v)| (k.clone(), v.as_str().unwrap().to_string()))
            .collect()
    };
    let mut graph = LockfileGraph::default();
    graph.overrides = strings(&input["lockedOverrides"]);
    for (name, pin) in input["runtimes"].as_object().into_iter().flatten() {
        graph.runtimes.insert(
            name.clone(),
            aube_lockfile::RuntimePin {
                specifier: pin["Specifier"].as_str().unwrap().to_string(),
                version: pin["Version"].as_str().unwrap().to_string(),
                dev: false,
                has_bin: false,
                variants: vec![],
            },
        );
    }
    graph.settings.auto_install_peers = input["autoPeers"].as_bool().unwrap();
    graph.pnpmfile_checksum = input["hook"].as_str().map(String::from);
    graph.ignored_optional_dependencies = input["ignored"]
        .as_array()
        .into_iter()
        .flatten()
        .map(|s| s.as_str().unwrap().to_string())
        .collect();
    graph
        .skipped_optional_dependencies
        .insert(".".into(), strings(&input["skipped"]));
    graph.importers.insert(
        ".".into(),
        input["deps"]
            .as_array()
            .into_iter()
            .flatten()
            .map(|d| DirectDep {
                name: d["Name"].as_str().unwrap().into(),
                dep_path: d["DepPath"].as_str().unwrap().into(),
                specifier: d["Specifier"].as_str().map(String::from),
                dep_type: match d["Type"].as_u64().unwrap() {
                    0 => DepType::Production,
                    1 => DepType::Dev,
                    _ => DepType::Optional,
                },
            })
            .collect(),
    );
    let mut manifests = vec![(".".to_string(), manifest)];
    for name in input["workspaceNames"].as_array().into_iter().flatten() {
        let name = name.as_str().unwrap();
        let path = format!("packages/{name}");
        let dep_path = format!("{name}@1.0.0");
        graph.packages.insert(
            dep_path.clone(),
            LockedPackage {
                name: name.into(),
                version: "1.0.0".into(),
                dep_path,
                local_source: Some(LocalSource::Link(path.clone().into())),
                ..Default::default()
            },
        );
        manifests.push((
            path,
            aube_manifest::PackageJson {
                name: Some(name.into()),
                ..Default::default()
            },
        ));
    }
    let catalogs = input["catalogs"]
        .as_object()
        .into_iter()
        .flatten()
        .map(|(name, v)| (name.clone(), strings(v)))
        .collect();
    let ignored: Vec<String> = input["workspaceIgnored"]
        .as_array()
        .into_iter()
        .flatten()
        .map(|s| s.as_str().unwrap().to_string())
        .collect();
    let kind = match input["kind"].as_str().unwrap_or("npm") {
        "pnpm" => LockfileKind::Pnpm,
        "nub" => LockfileKind::Aube,
        "bun" => LockfileKind::Bun,
        _ => LockfileKind::Npm,
    };
    let result = graph.check_drift_workspace_for_kind(
        &manifests,
        &strings(&input["overrides"]),
        &ignored,
        &catalogs,
        false,
        kind,
    );
    let reason = match result {
        DriftStatus::Fresh => String::new(),
        DriftStatus::Stale { reason } => reason,
    };
    println!("{}", serde_json::json!({"reason":reason}));
}
