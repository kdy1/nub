include!(env!("PM_GVS_ORACLE_SOURCE"));

pub fn run(path: &std::path::Path) {
    let cases: Vec<serde_json::Value> =
        serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap();
    let results: Vec<_> = cases.iter().map(|c| {
        if let Some(path) = c["directory"].as_str() {
            return serde_json::json!(detect_aube_dir_gvs_mode(std::path::Path::new(path)));
        }
        let linker = if c["Linker"] == "hoisted" { aube_linker::NodeLinker::Hoisted } else { aube_linker::NodeLinker::Isolated };
        let explicit = c["EnableGlobalVirtualStore"].as_bool();
        let hoist = c["HoistExplicit"].as_bool();
        if let Err(err) = reject_gvs_layout_contradiction(explicit,hoist,linker) {
            return serde_json::json!({"error":err.to_string()});
        }
        let env: Vec<(String,String)> = c["Env"].as_object().map(|m|m.iter().map(|(k,v)|(k.clone(),v.as_str().unwrap().to_string())).collect()).unwrap_or_default();
        let planned = planned_global_virtual_store(explicit,&env);
        let mode = Materialization::resolve(true,planned,c["ResolvedHoist"].as_bool().unwrap(),hoist,linker);
        serde_json::json!({"shared":mode.uses_shared_store(),"hidden":mode.build_hidden_tree(),"prewarm":prewarm_global_virtual_store_override(linker,mode.uses_shared_store(),explicit)})
    }).collect();
    println!("{}", serde_json::to_string(&results).unwrap());
}
