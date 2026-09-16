// Generated directly from the baseline's private state module at build time.
// No production executable includes or invokes this probe.
include!(env!("PM_STATE_ORACLE_SOURCE"));

pub fn run(path: &Path) {
    static PROFILE: aube_util::Embedder = aube_util::Embedder {
        manifest_namespace: "",
        ..aube_util::AUBE
    };
    aube_util::set_embedder(&PROFILE);
    let cases: Vec<serde_json::Value> =
        serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap();
    let results: Vec<_> = cases.iter().map(|case| {
        match case["op"].as_str().unwrap() {
            "state" => match serde_json::from_str::<InstallState>(case["raw"].as_str().unwrap()) {
                Ok(state) => serde_json::json!({"state":serde_json::to_string(&state).unwrap(), "fresh":serde_json::to_string(&FreshnessState::from(&state)).unwrap()}),
                Err(_) => serde_json::Value::Null,
            },
            "freshness" => {
                let state: FreshnessState = serde_json::from_str(case["raw"].as_str().unwrap()).unwrap();
                let project = Path::new(case["project"].as_str().unwrap());
                serde_json::json!({"manifest":package_jsons_stale(project,&state), "builds":deferred_dep_builds_stale(&state)})
            },
            "layout" => {
                let layout: InstallLayoutState = serde_json::from_str(case["raw"].as_str().unwrap()).unwrap();
                let project = Path::new(case["project"].as_str().unwrap());
                serde_json::json!({"reason": verify_install_layout(project, Some(&layout)), "gvs_current":gvs_nested_links_are_current(project,&layout)})
            },
            op => panic!("unknown state operation {op}"),
        }
    }).collect();
    println!("{}", serde_json::to_string(&results).unwrap());
}
