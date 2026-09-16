use serde_json::{Value, json};

pub fn urls(path: &std::path::Path) {
    let input: Value = serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap();
    let hosts: Vec<_> = input["urls"]
        .as_array()
        .unwrap()
        .iter()
        .map(|url| aube_registry::registry_host_key(url.as_str().unwrap()))
        .collect();
    let tarballs: Vec<_> = input["coordinates"]
        .as_array()
        .unwrap()
        .iter()
        .map(|case| {
            aube_registry::client::RegistryClient::new(case["registry"].as_str().unwrap())
                .tarball_url(
                    case["name"].as_str().unwrap(),
                    case["version"].as_str().unwrap(),
                )
        })
        .collect();
    println!("{}", json!({"hosts": hosts, "tarballs": tarballs}));
}
