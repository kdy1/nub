// Calls the unchanged engine's generated typed accessors, never Go metadata.
include!(env!("PM_SETTINGS_ORACLE_SOURCE"));

fn init_profile() {
    static PROFILE: aube_util::Embedder = aube_util::Embedder {
        read_branded_settings_env: false,
        unsupported_settings: NUB_UNSUPPORTED_SETTINGS,
        ..aube_util::AUBE
    };
    aube_util::set_embedder(&PROFILE);
}

pub fn run(path: &std::path::Path) {
    init_profile();
    let cases: Vec<serde_json::Value> =
        serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap();
    let results: Vec<_> = cases
        .iter()
        .map(|case| {
            let pairs = |key: &str| -> Vec<(String, String)> {
                case[key]
                    .as_array()
                    .map(|items| {
                        items
                            .iter()
                            .map(|p| (p[0].as_str().unwrap().into(), p[1].as_str().unwrap().into()))
                            .collect()
                    })
                    .unwrap_or_default()
            };
            let pnpm = case["pnpm"].as_bool().unwrap_or(false);
            aube_util::update_engine_context(|c| {
                c.read_branded_pnpm_config = pnpm;
                c.read_layout_from_workspace_yaml = false;
            });
            let cli = pairs("cli");
            let env = pairs("env");
            let project_config = pairs("projectConfig");
            let project_npmrc = pairs("projectNpmrc");
            let user_npmrc = pairs("userNpmrc");
            let project_tool = pairs("projectToolConfig");
            let user_tool = pairs("userToolConfig");
            let defaults = pairs("defaults");
            let managed = pairs("managed");
            let ws = if pnpm {
                case["workspaceYAML"].as_str().unwrap_or("{}")
            } else {
                "{}"
            };
            let global = if pnpm {
                case["globalYAML"].as_str().unwrap_or("{}")
            } else {
                "{}"
            };
            let workspace_yaml = yaml_serde::from_str(ws).unwrap();
            let global_config_yaml = yaml_serde::from_str(global).unwrap();
            let ctx = aube_settings::ResolveCtx {
                managed_aube_config: &managed,
                project_aube_config: &project_tool,
                project_npmrc: &project_npmrc,
                project_config: &project_config,
                user_aube_config: &user_tool,
                user_npmrc: &user_npmrc,
                workspace_yaml: &workspace_yaml,
                global_config_yaml: &global_config_yaml,
                env: &env,
                cli: &cli,
                embedder_defaults: &defaults,
            };
            resolved(
                case["name"].as_str().unwrap(),
                case["explicit"].as_bool().unwrap_or(false),
                &ctx,
            )
        })
        .collect();
    println!("{}", serde_json::to_string(&results).unwrap());
}

pub fn sources(path: &std::path::Path) {
    init_profile();
    aube_util::update_engine_context(|c| {
        c.read_branded_pnpm_config = true;
        c.read_layout_from_workspace_yaml = false;
    });
    let cases: Vec<String> = serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap();
    let results: Vec<_> = cases.iter().map(|raw| {
        let workspace_yaml: std::collections::BTreeMap<String, yaml_serde::Value> = match yaml_serde::from_str(raw) {
            Ok(map) => map,
            Err(_) => return serde_json::json!({"accepted":false}),
        };
        let empty = std::collections::BTreeMap::new();
        let ctx = aube_settings::ResolveCtx {
            managed_aube_config: &[], project_aube_config: &[], project_npmrc: &[],
            project_config: &[], user_aube_config: &[], user_npmrc: &[],
            workspace_yaml: &workspace_yaml, global_config_yaml: &empty,
            env: &[], cli: &[], embedder_defaults: &[],
        };
        let names = ["savePrefix","networkConcurrency","autoInstallPeers","minimumReleaseAgeExclude","updateConfig.ignoreDependencies"];
        let values: Vec<_> = names.iter().map(|name| resolved(name, false, &ctx)).collect();
        serde_json::json!({"accepted":true,"keys":workspace_yaml.keys().collect::<Vec<_>>(),"values":values})
    }).collect();
    println!("{}", serde_json::to_string(&results).unwrap());
}
