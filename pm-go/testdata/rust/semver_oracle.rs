pub fn run(path: &std::path::Path) {
    let data: serde_json::Value = serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap();
    let versions: Vec<_> = data["Versions"]
        .as_array()
        .unwrap()
        .iter()
        .map(|v| node_semver::Version::parse(v.as_str().unwrap()).ok())
        .collect();
    let ranges: Vec<_> = data["Ranges"]
        .as_array()
        .unwrap()
        .iter()
        .map(|v| node_semver::Range::parse(v.as_str().unwrap()).ok())
        .collect();
    let contains: Vec<Vec<bool>> = ranges
        .iter()
        .map(|range| {
            versions
                .iter()
                .map(|version| match (range, version) {
                    (Some(range), Some(version)) => version.satisfies(range),
                    _ => false,
                })
                .collect()
        })
        .collect();
    let intersects: Vec<Vec<bool>> = ranges
        .iter()
        .map(|a| {
            ranges
                .iter()
                .map(|b| match (a, b) {
                    (Some(a), Some(b)) => a.allows_any(b),
                    _ => false,
                })
                .collect()
        })
        .collect();
    println!(
        "{}",
        serde_json::json!({
            "versions":versions.iter().map(Option::is_some).collect::<Vec<_>>(),
            "ranges":ranges.iter().map(Option::is_some).collect::<Vec<_>>(),
            "contains":contains,
            "intersects":intersects,
        })
    );
}
