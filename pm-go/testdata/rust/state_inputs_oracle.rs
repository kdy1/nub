use std::path::Path;

pub fn directories(path: &Path) {
    let dirs: Vec<String> = serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap();
    let results: Vec<_> = dirs
        .iter()
        .map(|dir| {
            let dir = Path::new(dir);
            let pair = aube_store::directory_fingerprints(dir).unwrap();
            assert_eq!(
                pair.0,
                aube_store::directory_content_fingerprint(dir).unwrap()
            );
            assert_eq!(
                pair.1,
                aube_store::directory_metadata_fingerprint(dir).unwrap()
            );
            pair
        })
        .collect();
    println!("{}", serde_json::to_string(&results).unwrap());
}

pub fn manifests(path: &Path) {
    static PROFILE: aube_util::Embedder = aube_util::Embedder {
        manifest_namespace: "",
        ..aube_util::AUBE
    };
    aube_util::set_embedder(&PROFILE);
    let cases: Vec<String> = serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap();
    let results: Vec<String> = cases
        .iter()
        .map(|case| {
            // Parse the original manifest once. Round-tripping a Value through
            // JSON before crossing crate identities can change f64 rounding.
            let input = case.parse().unwrap();
            aube_util::hash::manifest_install_shape_digest(&input)
                .iter()
                .map(|b| format!("{b:02x}"))
                .collect()
        })
        .collect();
    println!("{}", serde_json::to_string(&results).unwrap());
}
