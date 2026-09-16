use serde_json::{Value, json};

pub fn run(path: &std::path::Path) {
    let cases: Vec<Value> = serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap();
    let runtime = tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()
        .unwrap();
    let results: Vec<Value> = runtime.block_on(async {
        let mut results = Vec::new();
        for case in cases {
            let client = std::sync::Arc::new(aube_registry::client::RegistryClient::new(
                case["registry"].as_str().unwrap(),
            ));
            let mode = match case["mode"].as_u64().unwrap() {
                1 => aube_resolver::ResolutionMode::TimeBased,
                2 => aube_resolver::ResolutionMode::LowestDirect,
                _ => aube_resolver::ResolutionMode::Highest,
            };
            let mut resolver = aube_resolver::Resolver::new(client)
                .with_project_root(case["root"].as_str().unwrap().into())
                .with_resolution_mode(mode)
                .with_named_registries(std::collections::BTreeMap::from([(
                    "private".into(),
                    case["registry"].as_str().unwrap().into(),
                )]))
                .with_auto_install_peers(case["autoPeers"].as_bool().unwrap())
                .with_workspace_member_importers(std::collections::BTreeMap::from([(
                    "local".into(),
                    "local".into(),
                )]))
                .with_catalogs(std::collections::BTreeMap::from([(
                    "default".into(),
                    std::collections::BTreeMap::from([("child".into(), "^1".into())]),
                )]))
                .with_overrides(std::collections::BTreeMap::from([(
                    "parent>child".into(),
                    "1.0.0".into(),
                )]));
            let manifest =
                aube_manifest::PackageJson::from_slice(case["manifest"].to_string().as_bytes())
                    .unwrap();
            let manifests = [(".".to_string(), manifest)];
            let workspace =
                std::collections::HashMap::from([("local".to_string(), "3.2.1".to_string())]);
            let mut result = resolver
                .resolve_workspace(&manifests, None, &workspace)
                .await;
            if case["reuse"].as_bool().unwrap() {
                if let Ok(graph) = result {
                    result = resolver
                        .resolve_workspace(&manifests, Some(&graph), &workspace)
                        .await;
                }
            }
            results.push(match result {
                Ok(graph) => json!({"ok":true,"graph":crate::graph_snapshot::snapshot(&graph)}),
                Err(e) => json!({"ok":false,"error":e.to_string()}),
            });
        }
        results
    });
    println!("{}", serde_json::to_string(&results).unwrap());
}
