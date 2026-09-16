// Test-only full graph projection. It does not change the Rust libraries.
// Cargo may build serde for host and target with distinct crate identities.
// Transfer foreign JSON values by their JSON bytes, and SmallVec by slices.
use aube_lockfile::{LocalSource, LockfileGraph};
use serde_json::{Map, Value, json};

fn source(s: &LocalSource) -> Value {
    let (kind, path) = match s {
        LocalSource::Directory(p) => (0, p.to_string_lossy().into_owned()),
        LocalSource::Tarball(p) => (1, p.to_string_lossy().into_owned()),
        LocalSource::Link(p) => (2, p.to_string_lossy().into_owned()),
        LocalSource::Portal(p) => (3, p.to_string_lossy().into_owned()),
        LocalSource::Exec(p) => (4, p.to_string_lossy().into_owned()),
        LocalSource::Git(_) => (5, String::new()),
        LocalSource::RemoteTarball(_) => (6, String::new()),
    };
    let mut out = json!({"Kind":kind,"Path":path,"URL":"","Resolved":"","Committish":null,"Integrity":null,"Subpath":null,"GitHosted":false});
    match s {
        LocalSource::Git(g) => {
            out["URL"] = json!(g.url);
            out["Resolved"] = json!(g.resolved);
            out["Committish"] = json!(g.committish);
            out["Integrity"] = json!(g.integrity);
            out["Subpath"] = json!(g.subpath);
        }
        LocalSource::RemoteTarball(t) => {
            out["URL"] = json!(t.url);
            out["Integrity"] = json!(t.integrity);
            out["GitHosted"] = json!(t.git_hosted);
        }
        _ => {}
    }
    out
}
pub fn snapshot(g: &LockfileGraph) -> Value {
    let mut packages = Map::new();
    for (key, p) in &g.packages {
        let mut out = Map::new();
        out.insert("Name".into(), json!(p.name));
        out.insert("Version".into(), json!(p.version));
        out.insert("DepPath".into(), json!(p.dep_path));
        out.insert("Integrity".into(), json!(p.integrity));
        out.insert("Dependencies".into(), json!(p.dependencies));
        out.insert(
            "OptionalDependencies".into(),
            json!(p.optional_dependencies),
        );
        out.insert("PeerDependencies".into(), json!(p.peer_dependencies));
        out.insert("OS".into(), json!(p.os.as_slice()));
        out.insert("CPU".into(), json!(p.cpu.as_slice()));
        out.insert("Libc".into(), json!(p.libc.as_slice()));
        out.insert("BundledDependencies".into(), json!(p.bundled_dependencies));
        out.insert("TarballURL".into(), json!(p.tarball_url));
        out.insert("RegistryGitHosted".into(), json!(p.registry_git_hosted));
        out.insert("ForceTarballURL".into(), json!(p.force_tarball_url));
        out.insert("AliasOf".into(), json!(p.alias_of));
        out.insert("YarnChecksum".into(), json!(p.yarn_checksum));
        out.insert("Engines".into(), json!(p.engines));
        out.insert("Bin".into(), json!(p.bin));
        out.insert(
            "DeclaredDependencies".into(),
            json!(p.declared_dependencies),
        );
        out.insert("License".into(), json!(p.license));
        out.insert("FundingURL".into(), json!(p.funding_url));
        out.insert("Optional".into(), json!(p.optional));
        out.insert(
            "TransitivePeerDependencies".into(),
            json!(p.transitive_peer_dependencies),
        );
        out.insert(
            "ExtraMeta".into(),
            Value::Object(
                p.extra_meta
                    .iter()
                    .map(|(k, v)| {
                        (
                            k.clone(),
                            serde_json::from_str(&v.to_string()).expect("reference JSON value"),
                        )
                    })
                    .collect(),
            ),
        );
        out.insert("HasInstallScript".into(), json!(p.has_install_script));
        out.insert("HasShrinkwrap".into(), json!(p.has_shrinkwrap));
        out.insert("InBundle".into(), json!(p.in_bundle));
        out.insert("Deprecated".into(), json!(p.deprecated));
        out.insert(
            "Source".into(),
            p.local_source.as_ref().map(source).unwrap_or(Value::Null),
        );
        out.insert(
            "PeerDependenciesMeta".into(),
            Value::Object(
                p.peer_dependencies_meta
                    .iter()
                    .map(|(k, v)| (k.clone(), json!({"Optional":v.optional})))
                    .collect(),
            ),
        );
        packages.insert(key.clone(), Value::Object(out));
    }
    let importers: Map<_, _> = g.importers.iter().map(|(k, deps)| (k.clone(), json!(deps.iter().map(|d| {
        let kind = match d.dep_type { aube_lockfile::DepType::Production => 0, aube_lockfile::DepType::Dev => 1, aube_lockfile::DepType::Optional => 2 };
        json!({"Name":d.name,"DepPath":d.dep_path,"Type":kind,"Specifier":d.specifier})
    }).collect::<Vec<_>>()))).collect();
    let catalogs: Map<_, _> = g
        .catalogs
        .iter()
        .map(|(k, entries)| {
            (
                k.clone(),
                Value::Object(
                    entries
                        .iter()
                        .map(|(k, v)| {
                            (
                                k.clone(),
                                json!({"Specifier":v.specifier,"Version":v.version}),
                            )
                        })
                        .collect(),
                ),
            )
        })
        .collect();
    let runtimes: Map<_, _> = g.runtimes.iter().map(|(k,p)| (k.clone(), json!({
        "Specifier":p.specifier,"Version":p.version,"Dev":p.dev,"HasBin":p.has_bin,
        "Variants":p.variants.iter().map(|v| json!({
            "Targets":v.targets.iter().map(|t| json!({"OS":t.os,"CPU":t.cpu,"Libc":t.libc})).collect::<Vec<_>>(),
            "Archive":v.archive,"URL":v.url,"Integrity":v.integrity,"Bin":v.bin,"BinIsBareString":v.bin_is_bare_string,"Prefix":v.prefix,
        })).collect::<Vec<_>>()
    }))).collect();
    let mut out = Map::new();
    out.insert("Packages".into(), Value::Object(packages));
    out.insert("Importers".into(), Value::Object(importers));
    out.insert("Catalogs".into(), Value::Object(catalogs));
    out.insert("Runtimes".into(), Value::Object(runtimes));
    out.insert(
        "IgnoredOptionalDependencies".into(),
        Value::Object(
            g.ignored_optional_dependencies
                .iter()
                .map(|k| (k.clone(), json!({})))
                .collect(),
        ),
    );
    out.insert("Settings".into(), json!({"AutoInstallPeers":g.settings.auto_install_peers,"ExcludeLinksFromLockfile":g.settings.exclude_links_from_lockfile,"IncludeTarballURL":g.settings.lockfile_include_tarball_url}));
    out.insert("Overrides".into(), json!(g.overrides));
    out.insert(
        "PackageExtensionsChecksum".into(),
        json!(g.package_extensions_checksum),
    );
    out.insert("PnpmfileChecksum".into(), json!(g.pnpmfile_checksum));
    out.insert("Times".into(), json!(g.times));
    out.insert(
        "SkippedOptionalDependencies".into(),
        json!(g.skipped_optional_dependencies),
    );
    out.insert("BunConfigVersion".into(), json!(g.bun_config_version));
    out.insert("PatchedDependencies".into(), json!(g.patched_dependencies));
    out.insert(
        "PatchedDependencyHashes".into(),
        json!(g.patched_dependency_hashes),
    );
    out.insert("TrustedDependencies".into(), json!(g.trusted_dependencies));
    out.insert(
        "ExtraFields".into(),
        Value::Object(
            g.extra_fields
                .iter()
                .map(|(k, v)| {
                    (
                        k.clone(),
                        serde_json::from_str(&v.to_string()).expect("reference JSON value"),
                    )
                })
                .collect(),
        ),
    );
    out.insert(
        "WorkspaceExtraFields".into(),
        Value::Object(
            g.workspace_extra_fields
                .iter()
                .map(|(k, m)| {
                    (
                        k.clone(),
                        Value::Object(
                            m.iter()
                                .map(|(k, v)| {
                                    (
                                        k.clone(),
                                        serde_json::from_str(&v.to_string())
                                            .expect("reference JSON value"),
                                    )
                                })
                                .collect(),
                        ),
                    )
                })
                .collect(),
        ),
    );
    let identity = aube_lockfile::graph_hash::graph_identity_hash(g, &|_| false);
    out.insert(
        "IdentityHash".into(),
        json!(
            identity
                .iter()
                .map(|b| format!("{b:02x}"))
                .collect::<String>()
        ),
    );
    out.insert(
        "NodeHashes".into(),
        json!(aube_lockfile::graph_hash::compute_graph_hashes(g, &|_| false, None).node_hash),
    );
    let mut affected: Vec<_> = aube_lockfile::graph_hash::content_affected_dep_paths(g)
        .into_iter()
        .collect();
    affected.sort();
    out.insert("ContentAffected".into(), json!(affected));
    Value::Object(out)
}
