use serde_json::Value;

pub fn filenames(path: &std::path::Path) {
    let cases: Vec<Value> = serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap();
    let results: Vec<_> = cases
        .iter()
        .map(|case| {
            aube_lockfile::dep_path_filename::dep_path_to_filename(
                case["path"].as_str().unwrap(),
                case["limit"].as_u64().unwrap() as usize,
            )
        })
        .collect();
    println!("{}", serde_json::to_string(&results).unwrap());
}
