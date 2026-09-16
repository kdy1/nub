use aube_resolver::override_rule::{self, AncestorFrame};
use std::collections::BTreeMap;

pub fn run(path: &std::path::Path) {
    let data: serde_json::Value = serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap();
    let raw: BTreeMap<String, String> = data["Rules"]
        .as_object()
        .unwrap()
        .iter()
        .map(|(k, v)| (k.clone(), v.as_str().unwrap().to_string()))
        .collect();
    let rules = override_rule::compile(&raw);
    let segments =
        |s: &override_rule::Segment| serde_json::json!({"Name":s.name,"Requirement":s.version_req});
    let compiled: Vec<_> = rules.iter().map(|r|serde_json::json!({"Parents":r.parents.iter().map(segments).collect::<Vec<_>>(),"Target":segments(&r.target),"Replacement":r.replacement,"RawKey":r.raw_key})).collect();
    let matches: Vec<Vec<bool>> = data["Tasks"]
        .as_array()
        .unwrap()
        .iter()
        .map(|task| {
            let ancestors: Vec<_> = task["Ancestors"]
                .as_array()
                .unwrap()
                .iter()
                .map(|a| AncestorFrame {
                    name: a["Name"].as_str().unwrap(),
                    version: a["Version"].as_str().unwrap(),
                })
                .collect();
            rules
                .iter()
                .map(|r| {
                    override_rule::matches(
                        r,
                        task["Name"].as_str().unwrap(),
                        task["Range"].as_str().unwrap(),
                        &ancestors,
                    )
                })
                .collect()
        })
        .collect();
    println!(
        "{}",
        serde_json::json!({"rules":compiled,"matches":matches})
    );
}
