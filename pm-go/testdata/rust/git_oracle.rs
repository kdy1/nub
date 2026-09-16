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

pub fn codeload(path: &std::path::Path) {
    let cases: Vec<Value> = serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap();
    let results: Vec<Value> = cases.iter().map(|case| {
        let url = case["url"].as_str().unwrap();
        let commit = case["commit"].as_str().unwrap();
        let integrity = case["integrity"].as_str();
        let bytes = std::fs::read(case["archive"].as_str().unwrap()).unwrap();
        let (tree, sha) = aube_store::extract_codeload_tarball(&bytes, url, commit, integrity).unwrap();
        let mut files = std::collections::BTreeMap::new();
        snapshot(&tree, &tree, &mut files);
        json!({"sha":sha, "files":files,
            "key": tree.file_name().unwrap().to_str().unwrap().strip_prefix("aube-codeload-").unwrap(),
            "integrity":aube_store::codeload_cache_integrity(url, commit, integrity)})
    }).collect();
    println!("{}", serde_json::to_string(&results).unwrap());
}

fn snapshot(
    root: &std::path::Path,
    dir: &std::path::Path,
    files: &mut std::collections::BTreeMap<String, Value>,
) {
    for entry in std::fs::read_dir(dir).unwrap() {
        let entry = entry.unwrap();
        let path = entry.path();
        let meta = std::fs::symlink_metadata(&path).unwrap();
        let relative = path
            .strip_prefix(root)
            .unwrap()
            .to_str()
            .unwrap()
            .replace('\\', "/");
        if meta.file_type().is_symlink() {
            files.insert(relative, json!({"kind":"link", "target":std::fs::read_link(&path).unwrap().to_str().unwrap()}));
        } else if meta.is_dir() {
            files.insert(relative, json!({"kind":"directory"}));
            snapshot(root, &path, files);
        } else {
            #[cfg(unix)]
            let mode = {
                use std::os::unix::fs::PermissionsExt;
                meta.permissions().mode() & 0o7777
            };
            #[cfg(not(unix))]
            let mode = 0;
            files.insert(relative, json!({"kind":"file", "content":std::fs::read_to_string(&path).unwrap(), "mode":mode}));
        }
    }
}
