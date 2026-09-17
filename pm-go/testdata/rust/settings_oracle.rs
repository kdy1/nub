// Calls the unchanged engine's generated typed accessors, never Go metadata.
include!(env!("PM_SETTINGS_ORACLE_SOURCE"));

pub fn run(path: &std::path::Path) {
    static PROFILE: aube_util::Embedder = aube_util::Embedder {
        read_branded_settings_env: false,
        ..aube_util::AUBE
    };
    aube_util::set_embedder(&PROFILE);
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
