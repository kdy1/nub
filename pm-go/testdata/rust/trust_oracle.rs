// Test-only trust-policy and package-version-policy adapter.
use aube_registry::{Attestations, Dist, NpmUser, Packument, VersionMetadata};
use serde_json::{Value, json};
use std::collections::BTreeMap;

fn version(v: &Value) -> VersionMetadata {
    VersionMetadata {
        name: v["name"].as_str().unwrap().into(),
        version: v["version"].as_str().unwrap().into(),
        dependencies: BTreeMap::new(),
        dev_dependencies: BTreeMap::new(),
        optional_dependencies: BTreeMap::new(),
        peer_dependencies: BTreeMap::new(),
        peer_dependencies_meta: BTreeMap::new(),
        bundled_dependencies: None,
        os: vec![],
        cpu: vec![],
        libc: vec![],
        engines: BTreeMap::new(),
        license: None,
        funding_url: None,
        bin: BTreeMap::new(),
        has_install_script: false,
        deprecated: None,
        // Parse inferred foreign Value types through FromStr, avoiding Cargo's
        // host/target serde crate identity split in test-probe artifacts.
        approver: (!v["approver"].is_null()).then(|| v["approver"].to_string().parse().unwrap()),
        npm_user: v["_npmUser"].as_object().map(|user| NpmUser {
            trusted_publisher: user
                .get("trustedPublisher")
                .filter(|x| !x.is_null())
                .map(|x| x.to_string().parse().unwrap()),
        }),
        dist: v["dist"].as_object().map(|dist| Dist {
            tarball: dist
                .get("tarball")
                .and_then(Value::as_str)
                .unwrap_or("")
                .into(),
            integrity: None,
            shasum: None,
            unpacked_size: None,
            attestations: dist
                .get("attestations")
                .and_then(Value::as_object)
                .map(|a| Attestations {
                    provenance: a
                        .get("provenance")
                        .filter(|x| !x.is_null())
                        .map(|x| x.to_string().parse().unwrap()),
                }),
        }),
    }
}
fn patterns(v: &Value) -> Vec<&str> {
    v.as_array()
        .unwrap()
        .iter()
        .map(|v| v.as_str().unwrap())
        .collect()
}
pub fn policy(path: &std::path::Path) {
    let inputs: Value = serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap();
    let outputs:Vec<_>=inputs.as_array().unwrap().iter().map(|i| {
        let result=if i["Default"].as_bool().unwrap() {Ok(aube_resolver::TrustExcludeRules::default())}else {aube_resolver::TrustExcludeRules::parse(patterns(&i["Patterns"]))};
        match result {
            Ok(p)=>json!({"Len":p.len(),"Error":null,"Matches":i["Tasks"].as_array().unwrap().iter().map(|t|p.excludes(t["Name"].as_str().unwrap(),t["Version"].as_str().unwrap())).collect::<Vec<_>>()}),
            Err(e)=>json!({"Len":0,"Error":e.to_string(),"Matches":null}),
        }
    }).collect();
    println!("{}", json!(outputs));
}
pub fn trust(path: &std::path::Path) {
    let inputs: Value = serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap();
    let outputs:Vec<_>=inputs.as_array().unwrap().iter().map(|i| {
        let raw=&i["Packument"];
        let p=Packument{name:raw["name"].as_str().unwrap().into(),modified:None,dist_tags:BTreeMap::new(),time:raw["time"].as_object().unwrap().iter().map(|(k,v)|(k.clone(),v.as_str().unwrap().into())).collect(),versions:raw["versions"].as_object().unwrap().iter().map(|(k,v)|(k.clone(),version(v))).collect()};
        let picked=i["Picked"].as_str().unwrap();let meta=&p.versions[picked];
        let excludes=aube_resolver::TrustExcludeRules::parse(patterns(&i["Excludes"])).unwrap();
        let error=aube_resolver::check_no_downgrade(&p,picked,meta,&excludes,i["IgnoreAfterMinutes"].as_u64()).err().map(|e|match e {
            aube_resolver::TrustCheckError::Downgrade(d)=>aube_resolver::Error::TrustDowngrade(Box::new(d)).to_string(),
            aube_resolver::TrustCheckError::MissingTime(d)=>aube_resolver::Error::TrustCheckMissingTime(Box::new(d)).to_string(),
        });
        let prior=aube_resolver::strongest_prior_evidence(&p,picked).map(|p|json!({"Version":p.version,"Evidence":p.evidence.rank()}));
        json!({"Evidence":aube_resolver::evidence_for(meta).map_or(0,|e|e.rank()),"Prior":prior,"Error":error})
    }).collect();
    println!("{}", json!(outputs));
}
