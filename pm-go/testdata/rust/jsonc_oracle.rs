include!(env!("PM_JSONC_READ_ORACLE_SOURCE"));

pub fn reads(path: &std::path::Path) {
    let cases: Vec<serde_json::Value> =
        serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap();
    let results: Vec<_> = cases
        .iter()
        .map(|c| {
            let result = if let Some(path) = c["path"].as_str() {
                read_guarded(std::path::Path::new(path)).map_err(|e| e.to_string())
            } else {
                nub_json_guard::check_nesting_depth(
                    c["text"].as_str().unwrap(),
                    c["depth"].as_u64().unwrap() as usize,
                )
                .map(|()| String::new())
            };
            match result {
                Ok(value) => serde_json::json!({"value":value,"error":""}),
                Err(error) => serde_json::json!({"value":"","error":error}),
            }
        })
        .collect();
    println!("{}", serde_json::to_string(&results).unwrap());
}
