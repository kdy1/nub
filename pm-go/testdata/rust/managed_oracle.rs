include!(env!("PM_MANAGED_ORACLE_SOURCE"));

pub fn run(path: &std::path::Path) {
    let cases: Vec<String> = serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap();
    let results: Vec<_> = cases
        .iter()
        .map(|raw| match raw.parse::<DocumentMut>() {
            Ok(document) => {
                serde_json::json!({"Accepted":true,"Entries":AubeConfigEdit {document}.entries()})
            }
            Err(_) => serde_json::json!({"Accepted":false,"Entries":null}),
        })
        .collect();
    println!("{}", serde_json::to_string(&results).unwrap());
}
