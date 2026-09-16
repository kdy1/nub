include!(env!("PM_DELTA_ORACLE_SOURCE"));

pub fn run(path: &Path) {
    let cases: Vec<serde_json::Value> =
        serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap();
    let results: Vec<_> = cases.iter().map(|case| {
        let mut graph = crate::peer_oracle::graph(&case["graph"]);
        for (key,pkg) in &mut graph.packages {
            let p = &case["graph"]["Packages"][key];
            pkg.integrity = p["Integrity"].as_str().map(String::from);
            pkg.alias_of = p["AliasOf"].as_str().map(String::from);
            pkg.tarball_url = p["TarballURL"].as_str().map(String::from);
            pkg.registry_git_hosted = p["RegistryGitHosted"].as_bool().unwrap();
            let strings = |v: &serde_json::Value| v.as_array().map(|a|a.iter().map(|s|s.as_str().unwrap().to_string()).collect()).unwrap_or_default();
            pkg.os = strings(&p["OS"]); pkg.cpu = strings(&p["CPU"]); pkg.libc = strings(&p["Libc"]);
        }
        let patches: BTreeMap<String,String> = serde_json::from_value(case["patches"].clone()).unwrap();
        let selected: BTreeSet<String> = serde_json::from_value(case["selected"].clone()).unwrap();
        let stored: BTreeMap<String,String> = serde_json::from_value(case["stored"].clone()).unwrap();
        let (leaf,subtree) = compute_leaf_and_subtree_hashes(&graph,&patches,Path::new(case["project"].as_str().unwrap()));
        let plan = diff(&stored,&leaf);
        let mut hash = lthash_of(&leaf);
        let original = hex::encode(hash.digest());
        hash.add("duplicate"); hash.add("duplicate"); hash.remove("duplicate");
        serde_json::json!({"leaf":leaf,"subtree":subtree,"digest":original,"incremented":hex::encode(hash.digest()),"phases":dependency_build_phases(&graph,&selected),"added":plan.added,"removed":plan.removed,"changed":plan.changed,"touch":plan.touched_set(),"roots":changed_subtree_roots(&stored,&subtree)})
    }).collect();
    println!("{}", serde_json::to_string(&results).unwrap());
}
