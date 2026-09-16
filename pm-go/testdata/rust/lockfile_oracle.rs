// Test-only adapter to the unchanged Rust lockfile library. This is not linked
// into, shipped with, or invoked by the Go package manager.
fn main() {
    let args: Vec<_> = std::env::args_os().skip(1).collect();
    assert_eq!(args.len(), 2, "expected lockfile and manifest paths");
    let path = std::path::Path::new(&args[0]);
    let manifest = aube_manifest::PackageJson::from_path(std::path::Path::new(&args[1]))
        .expect("read reference manifest");
    let graph = aube_lockfile::npm::parse(path, &manifest).expect("read reference lockfile");
    aube_lockfile::npm::write(path, &graph, &manifest).expect("write reference lockfile");
}
