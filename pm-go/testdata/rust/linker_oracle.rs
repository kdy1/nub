use serde_json::Value;
use std::path::Path;

pub fn bins(path: &Path) {
    let cases: Vec<Value> = serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap();
    let results: Vec<_> = cases.iter().map(bin_case).collect();
    println!("{}", serde_json::to_string(&results).unwrap());
}

fn bin_case(case: &Value) -> Value {
    use aube_linker::{BinShimOptions, create_bin_shim, validate_bin_name, validate_bin_target};
    match case["op"].as_str().unwrap() {
        "name" => {
            serde_json::json!({"valid":validate_bin_name(case["value"].as_str().unwrap()).is_ok()})
        }
        "target" => {
            serde_json::json!({"valid":validate_bin_target(case["value"].as_str().unwrap()).is_ok()})
        }
        "create" => {
            let bin_dir = Path::new(case["bin_dir"].as_str().unwrap());
            let name = case["name"].as_str().unwrap();
            let target = Path::new(case["target"].as_str().unwrap());
            let hidden = case["hidden"].as_str().map(Path::new);
            create_bin_shim(
                bin_dir,
                name,
                target,
                BinShimOptions {
                    extend_node_path: case["extend"].as_bool().unwrap_or(false),
                    prefer_symlinked_executables: case["symlink"].as_bool(),
                    hidden_modules_dir: hidden,
                },
            )
            .unwrap();
            let mut files = serde_json::Map::new();
            #[cfg(windows)]
            let suffixes = ["", ".cmd", ".ps1"].as_slice();
            #[cfg(not(windows))]
            let suffixes = [""].as_slice();
            for suffix in suffixes {
                let path = bin_dir.join(format!("{name}{suffix}"));
                if let Ok(info) = std::fs::symlink_metadata(&path) {
                    let mut entry = serde_json::Map::new();
                    if info.file_type().is_symlink() {
                        entry.insert(
                            "link".into(),
                            serde_json::json!(std::fs::read_link(&path).unwrap().to_string_lossy()),
                        );
                    } else {
                        entry.insert(
                            "body".into(),
                            serde_json::json!(std::fs::read_to_string(&path).unwrap()),
                        );
                    }
                    #[cfg(unix)]
                    {
                        use std::os::unix::fs::PermissionsExt;
                        entry.insert(
                            "mode".into(),
                            serde_json::json!(info.permissions().mode() & 0o777),
                        );
                    }
                    files.insert(suffix.to_string(), Value::Object(entry));
                }
            }
            let mut result = serde_json::Map::new();
            result.insert("files".into(), Value::Object(files));
            #[cfg(unix)]
            {
                use std::os::unix::fs::PermissionsExt;
                if let Ok(info) = std::fs::metadata(target) {
                    result.insert(
                        "target_mode".into(),
                        serde_json::json!(info.permissions().mode() & 0o777),
                    );
                }
            }
            Value::Object(result)
        }
        "read" => {
            let path = Path::new(case["path"].as_str().unwrap());
            let content = std::fs::read_to_string(path).unwrap();
            let resolved = aube_linker::sys::resolve_bin_shim(path).unwrap().map(|shim| {
                serde_json::json!({"target":shim.target.to_string_lossy(), "node_path":shim.node_path.map(|p|p.to_string_lossy().to_string())})
            });
            serde_json::json!({"posix":aube_linker::parse_posix_shim_target(&content), "win":aube_linker::parse_win_shim_target(&content), "resolved":resolved})
        }
        op => panic!("unknown bin operation {op}"),
    }
}

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
