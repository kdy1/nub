# Go package manager

`nub-pm-go` is an independent Go implementation of Nub's package manager. It is under development; registered commands without a handler return a nonzero exit status. The existing Rust executable is unchanged.

Build from this directory with `go build -o ../target/pm-go/ ./cmd/nub-pm-go`. Run checks with `go test -race ./...` and `go vet ./...`; the Go package manager workflow runs them on Linux, macOS, and Windows.

The command inventory in `internal/surface/commands.json` is checked against Nub's Rust registry. Its reference revision is `2a4573ef059798b75f789aa8c22da51e470e3138`.

`internal/surface/arguments.json` records the matching Rust argument declarations and flattened structs. Regenerate it with `python3 pm-go/scripts/extract_surface.py` from the repository root. CI checks for drift. These declarations are inputs to the port; they do not by themselves establish which engine options the Nub adapter accepts.

See [COVERAGE.md](COVERAGE.md) for command, component, and validation coverage. The port is incomplete.

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

The npm lockfile reader supports v1/v2/v3 and versionless shrinkwraps. It reconstructs nested dependency and peer placement, distinguishes workspace members from local links, and preserves alias, source, platform, and package metadata. Legacy graphs with missing edges produce a diagnostic. The v3 writer rebuilds hoisted layouts, preserves valid existing root placements, and atomically writes package metadata and dependency flags in npm's key order. Tests reuse the reference's native npm fixtures for byte comparisons. Install integration remains pending.

CI also runs npm 11.19.0 against an isolated test registry: npm creates each lockfile, Go rewrites it, and npm performs a clean install and a second lockfile-only install. Alias, peer, optional, workspace, and remote-tarball fixtures check output and Node module resolution. A workspace conflict fixture records the reference writer's redundant member-local hoist explicitly; it is a known native-npm byte difference. Set `PM_NPM_CLI` to that version's `npm-cli.js` to enable this oracle outside CI. The reference job additionally compares Go's bytes with a test-only probe linked from the existing Rust build artifacts.

The Go executable uses its own `nub-pm-go` global cache and store namespace with the reference XDG/Windows directory precedence. Shared and exclusive file leases are cancellable and tested across processes. Lifecycle scripts and fetched tools use Node from `PATH`; Nub's runtime augmentation, TypeScript transformation, and Node provisioning are outside this executable.

The pnpm/nub.lock adapter reads v9+ graphs, scores pnpm 11 document streams, and preserves source, peer, patch, catalog, skipped optional, and runtime metadata. Its writer retains pnpm's section order and layout, translates source and alias keys, stamps matching patch hashes, and supports the existing native-lock alias representation. This remains an internal component; install integration and unchanged-lockfile policy are pending.

Set `PM_PNPM_CLI` to pnpm 10.15.1's `pnpm.cjs` to enable the isolated pnpm acceptance oracle. It generates alias/peer/optional, workspace, catalog, and remote-tarball locks, rewrites them with Go, and checks frozen installs and Node resolution. CI runs this with PATH Node and no Rust addon.

Shared JSON parsing rejects invalid UTF-8 and unpaired UTF-16 escapes. Typed lockfile readers can retain repeated object fields for format-specific duplicate checks, while manifest values keep last-value-wins behavior.

Bun's text-lockfile decoder preserves JSONC byte offsets, validates typed fields, retains unknown metadata, and distinguishes registry and Git tuple layouts. Binary `bun.lockb` is outside the reference reader's supported formats.

The Bun v1/v2 graph reader resolves nested/scoped package keys and workspace overrides, preserves catalogs and source identities, and retains required workspace peers. Unsupported required sources fail eagerly under Nub's policy; unsupported optionals emit diagnostics and preserve skipped-importer declarations. CI compares complete graphs with the Rust reader in both Nub's strict mode and standalone lenient mode.

The Bun writer emits v1 JSONC with the parsed config version, native field order, registry URLs, Git cache tags, workspace tuples, catalogs, and unknown metadata. It uses the reference's first-importer-wins root hoist behavior and reads member manifests when available. Native fixture bytes and Rust graph-to-writer output are checked separately from install integration.

Set `PM_BUN_BIN` to Bun 1.3.14 to run the isolated Bun acceptance oracle. It generates native locks for aliases/peers/optionals, workspaces, catalogs and remote tarballs, checks Go output, clears the fixture cache, and performs frozen installs followed by PATH Node resolution probes. The CI setup pins the Bun executable version and the setup action revision.

The reference Bun writer differs from native Bun for nested-key ordering and the empty registry slot in remote-tarball tuples. The oracle asserts these specific byte differences and compares unmodified Rust/Go output for the generated fixtures. Install-level preservation of an unchanged native file remains pending.

Yarn prerequisites include bounded Berry detection, the classic line tokenizer, npm-alias identity, pinned Git/local source classification, and private-registry URL retention. The classic tokenizer intentionally follows the reference's treatment of optional and peer sections.

Bun 1.3.14 rejects the reference writer's four-element remote-tarball tuple (`Expected an object`) during a frozen install. This fixture is a negative compatibility check, with exact Rust/Go bytes checked in the reference job. Alias/peer/optional, workspace and catalog fixtures require successful cold frozen installs. The remote-tarball result does not establish native acceptance.

The Yarn classic graph reader resolves aliases and pinned sources, reconstructs workspace importers from member manifests, links matching sibling workspaces, and applies Nub's required/optional unsupported-source policy. The Rust differential suite compares full graphs with required and optional root declarations in both strict and lenient modes. Berry reader/writer behavior is covered by the same differential suite.

The Yarn classic conversion writer groups exact and declared ranges, retains authored local/Git descriptors, quotes scoped dependency keys, and rejects Git conversion when the original declaration cannot be recovered. This writer is for explicit PM conversion; it does not enable ordinary writes to existing classic projects. Reader/writer output is compared with the unchanged Rust libraries.

Direct override matching preserves version-selector priority, pnpm/Yarn ancestor boundaries, and the reference lower-bound probe. Nested npm override objects can be flattened after callers select the permitted configuration sources; flattening does not read branded configuration itself.

The Berry reader accepts metadata versions 3 and later, preserves checksums and peer metadata, resolves patch descriptors and root resolution pins, and keeps optional transitive edges separate. Workspace importers are reconstructed from manifests. Callers can supply an identity-scoped override map; the low-level reader otherwise preserves the reference library source precedence. Unsupported sources retain the strict required/optional behavior. The Rust graph oracle includes the reference Berry fixtures.

The Berry writer emits reference metadata, root workspace descriptors, declared-range headers, patch resolutions, bins and peer metadata, sorted by header descriptor. It preserves the Rust writer’s canonical package collapse and source representation. Patch conflicts fail before the destination changes. Native Yarn acceptance and installation-level preservation are separate pending checks.

CI pins Yarn classic 1.22.22 and Berry 4.18.0 as additional test oracles. An isolated registry fixture checks two conflicting transitive versions, rewrites through Go, removes installed packages and cache, then checks frozen installation and PATH Node resolution. The Rust job also compares exact writer output for that native fixture.

Registry fixtures include fixed publication timestamps so native age gates operate on deterministic metadata. Tests can override each version’s timestamp to exercise release-age policy.

Lockfile freshness helpers compare patch configuration using each format’s recorded hash/path model, compare used catalog entries, and detect package-extension checksum changes. Workspace-wide metadata checks and install orchestration are still pending.

The reference Berry reader/writer drops npm resolution `__archiveUrl` qualifiers for nonstandard archive locations. Native tests assert that exact byte loss separately from the standard-registry acceptance case, and exercise the resulting cold-fetch failure. This is a recorded reference limitation; installation-level preservation of an unchanged lockfile remains pending.

Importer freshness checks preserve section-specific specifiers, optional skip records, auto-installed peer ranges, effective overrides, hook-derived local links and workspace-root link exemptions. An empty importer still detects newly added dependencies. Go cases are also evaluated through the unchanged Rust drift API in reference CI.
