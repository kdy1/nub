use aube::commands::install::{GlobalVirtualStoreFlags, InstallArgs};

pub fn run(path: &std::path::Path) {
    let cases: Vec<serde_json::Value> =
        serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap();
    let results: Vec<_> = cases.iter().map(|c| {
        let r = &c["Request"];
        let b = |key: &str| r[key].as_bool().unwrap_or(false);
        let s = |key: &str| r[key].as_str().map(str::to_string);
        let args = InstallArgs {
            dev: b("Dev"), prod: b("Prod"), no_optional: b("NoOptional"),
            dangerously_allow_all_builds: b("AllowAllBuilds"),
            dry_run: false, fix_lockfile: b("FixLockfile"), force: b("Force"),
            global_pnpmfile: None, ignore_pnpmfile: false, ignore_scripts: false,
            lockfile_dir: s("LockfileDir"), lockfile_only: b("LockfileOnly"),
            merge_git_branch_lockfiles: false, network_concurrency: r["NetworkConcurrency"].as_u64(),
            no_side_effects_cache: b("NoSideEffectsCache"),
            no_verify_store_integrity: b("NoVerifyStoreIntegrity"), node_linker: s("NodeLinker"),
            offline: b("Offline"), package_import_method: s("PackageImportMethod"),
            pnpmfile: None, prefer_offline: b("PreferOffline"),
            public_hoist_pattern: r["PublicHoistPattern"].as_array().map(|a| a.iter().map(|v|v.as_str().unwrap().to_string()).collect()).unwrap_or_default(),
            resolution_mode: s("ResolutionMode"), shamefully_hoist: b("ShamefullyHoist"),
            side_effects_cache: b("SideEffectsCache"), verify_store_integrity: b("VerifyStoreIntegrity"),
            workspace_root_short: false,
            lockfile: aube::cli_args::LockfileArgs {
                frozen_lockfile: r["Lockfile"]["Frozen"].as_bool().unwrap(),
                no_frozen_lockfile: r["Lockfile"]["NoFrozen"].as_bool().unwrap(),
                prefer_frozen_lockfile: r["Lockfile"]["PreferFrozen"].as_bool().unwrap(),
            },
            network: Default::default(), virtual_store: Default::default(),
        };
        let frozen = args.lockfile.frozen_override();
        let global = GlobalVirtualStoreFlags { enable: b("EnableGlobalStore"), disable: b("DisableGlobalStore") };
        let flags = args.to_cli_flag_bag(frozen, global);
        let options = args.into_options(frozen, c["Prefer"].as_bool(), flags.clone(), Vec::new());
        let deps = options.dep_selection;
        serde_json::json!({
            "mode":format!("{:?}",options.mode).to_ascii_lowercase(),
            "strict":options.strict_no_lockfile,
            "network":format!("{:?}",options.network_mode),
            "dev":deps.dev_only(),"prod":deps.prod_only(),"optional":deps.skip_optional(),
            "filtered":deps.is_filtered(),"axis":deps.prod_or_dev_axis(),"label":deps.label(),
            "skipRoot":options.skip_root_lifecycle,"devPreinstall":options.run_dev_preinstall,
            "flags":flags,"overrideFlag":frozen.map(|v|v.cli_flag()).unwrap_or(""),"gvsSet":global.is_set(),
        })
    }).collect();
    println!("{}", serde_json::to_string(&results).unwrap());
}
