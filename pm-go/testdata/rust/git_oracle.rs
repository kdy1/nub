use serde_json::{Value, json};

pub fn refs(path: &std::path::Path) {
    let cases: Vec<Value> = serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap();
    let results: Vec<Value> = cases
        .iter()
        .map(|case| {
            let url = case["url"].as_str().unwrap();
            if case["host"].as_bool().unwrap_or(false) {
                return json!({"host": aube_store::git_url_host(url)});
            }
            match aube_store::git_resolve_ref(url, case["ref"].as_str()) {
                Ok(sha) => json!({"ok":true, "sha":sha}),
                Err(e) => json!({"ok":false, "error":e.to_string()}),
            }
        })
        .collect();
    println!("{}", serde_json::to_string(&results).unwrap());
}
