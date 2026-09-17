// Validators and lowering functions are extracted from the unchanged adapter.
pub mod project_config {
    include!(env!("PM_NATIVE_INSTALL_ORACLE_SOURCE"));
}
include!(env!("PM_NATIVE_LOWER_ORACLE_SOURCE"));

pub fn run(path: &std::path::Path) {
    let cases: Vec<serde_json::Value> =
        serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap();
    let results: Vec<_> = cases
        .iter()
        .map(|c| {
            let raw: serde_json::Value = serde_json::from_str(c["raw"].as_str().unwrap()).unwrap();
            let config = match project_config::parse(&raw) {
                Ok(c) => c,
                Err(e) => {
                    use project_config::ConfigError::*;
                    return match e {
                        UnknownKey { path, key } => {
                            serde_json::json!({"error":{"path":path,"key":key}})
                        }
                        Type { path, expected } => {
                            serde_json::json!({"error":{"path":path,"expected":expected}})
                        }
                        Value { path, message } => {
                            serde_json::json!({"error":{"path":path,"message":message}})
                        }
                        other => panic!("unexpected install validation error {other:?}"),
                    };
                }
            };
            let defaults: Vec<(String, String)> = c["defaults"]
                .as_array()
                .unwrap()
                .iter()
                .map(|p| (p[0].as_str().unwrap().into(), p[1].as_str().unwrap().into()))
                .collect();
            match lower_native_install_settings(&config, &defaults) {
                Err(e) => serde_json::json!({"error":e.to_string()}),
                Ok(lowered) => {
                    let locality = if c["Local"].as_bool().unwrap() {
                        VirtualStoreLocality::ProjectLocal
                    } else {
                        VirtualStoreLocality::Default
                    };
                    let entries =
                        scoped_install_settings(&lowered, c["Native"].as_bool().unwrap(), locality);
                    serde_json::json!({"entries":entries,"eject":lowered.eject})
                }
            }
        })
        .collect();
    println!("{}", serde_json::to_string(&results).unwrap());
}
