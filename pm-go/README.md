# Go package manager

`nub-pm-go` is an independent Go implementation of Nub's package manager. It is under development; registered commands without a handler return a nonzero exit status. The existing Rust executable is unchanged.

Build from this directory with `go build -o ../target/pm-go/ ./cmd/nub-pm-go`. Run checks with `go test -race ./...` and `go vet ./...`; the Go package manager workflow runs them on Linux, macOS, and Windows.

The command inventory in `internal/surface/commands.json` is checked against Nub's Rust registry. Its reference revision is `2a4573ef059798b75f789aa8c22da51e470e3138`.

Implemented handlers: `pkg get/set/delete/fix` and `set-script` (`ss`). Manifest edits preserve key order, indentation, line endings, and trailing-newline style. Invalid multi-key edits leave the file unchanged. The Go workflow also builds the Rust reference and compares both executables on the same manifest fixtures.

The project model includes PM declaration and lockfile selection, major-specific override policy, and the npmrc registry configuration layer. Registry credentials retain their source scope; project files cannot expand environment secrets, configure token helpers or proxies, or disable TLS validation. These internal components are not yet connected to an installation handler.

The semver layer supports npm comparator sets, OR, partial and wildcard versions, hyphen ranges, tilde/caret ranges, and prerelease admission by version tuple. CI compares its results with the repository's pinned `node-semver` 7.7.4 fixture; Node is a test oracle and is not required for Go version resolution.

Registry metadata parsing preserves raw fields and normalizes legacy dependency, platform, bin, license, and funding shapes. Invalid integrity metadata is rejected. Version selection implements lockfile and dist-tag preferences, deprecation ordering, and publish-age gates, including the bounded fallback for an age-blocked `latest` tag.

Registry transport uses source-scoped credentials, TLS roots and client certificates, and explicit proxy settings. It blocks HTTPS downgrades, strips credentials across redirect authorities, limits response bodies, and bounds timeout retries. Tests use isolated HTTP and TLS servers.

Metadata caches separate registry URLs and full/abbreviated responses. They support offline and prefer-offline reads, conditional revalidation, atomic replacement, corruption recovery, and concurrent request deduplication. Cache locations are supplied by the caller.

The store integrity layer verifies SHA-1/256/384/512 SRI values using the strongest supported algorithm, including multiple digests and SRI options. Tarball manifest checks preserve the reference implementation's version normalization and name checks.

Tarball extraction strips the wrapper directory, rejects escaping paths and non-regular entries, and enforces decompressed-size, per-file, and entry-count limits. Extraction creates a new tree, preserves executable status, and removes partial output on failure. Windows filename checks apply on Windows.

Dependency helpers preserve scoped coordinate splitting, workspace alias/path/range grammar, and OS/CPU/libc constraints. Linux libc detection checks the active loader before installed loader files, including when the Go executable is built without cgo.

The content-addressed store uses BLAKE3 file keys, atomic publication, integrity-partitioned package indexes, and read-only fallback stores. CAS files remain non-executable; indexes carry per-package executable bits. Tests cover process concurrency, interrupted imports, truncated files, and maintenance locks.

Tarball and local-directory imports produce store indexes. Local directory imports skip `.git`, `node_modules`, and symlinks. Registry tarball downloads enforce URL schemes, offline policy, URI-scoped authentication, and response limits.

The lockfile source model preserves file/link/portal/exec, Git, and remote-tarball identities. It handles hosted Git clone/archive forms, pinned source keys, Git fragment selectors, and dependency-edge lookup across incumbent lockfile conventions. Source parsing does not execute generators or clone repositories.

The common dependency graph preserves resolution metadata through filters and workspace subsets. Graph passes compute reachability, ancestor closures, shortest depths, optional-only packages, platform filtering, and unresolved transitive peers, including cyclic and source-backed graphs.

The install-facing manifest model parses workspace string/array/object forms, catalogs, bundled dependencies, optional peers, and neutral dependency metadata. It preserves authored-empty workspace fields and applies the reference tolerance for legacy dependency, script, and engine data.

The npm lockfile reader supports v1/v2/v3 and versionless shrinkwraps. It reconstructs nested dependency and peer placement, distinguishes workspace members from local links, and preserves alias, source, platform, and package metadata. Legacy graphs with missing edges produce a diagnostic. Reading does not rewrite lockfiles; npm writing and install integration remain pending.

The Go executable uses its own `nub-pm-go` global cache and store namespace with the reference XDG/Windows directory precedence. Shared and exclusive file leases are cancellable and tested across processes. Lifecycle scripts and fetched tools use Node from `PATH`; Nub's runtime augmentation, TypeScript transformation, and Node provisioning are outside this executable.
