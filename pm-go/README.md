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

Lockfile freshness helpers compare patch configuration using each format’s recorded hash/path model, compare used catalog entries, and detect package-extension checksum changes. Install orchestration is still pending.

The reference Berry reader/writer drops npm resolution `__archiveUrl` qualifiers for nonstandard archive locations. Native tests assert that exact byte loss separately from the standard-registry acceptance case, and exercise the resulting cold-fetch failure. This is a recorded reference limitation; installation-level preservation of an unchanged lockfile remains pending.

Importer freshness checks preserve section-specific specifiers, optional skip records, auto-installed peer ranges, effective overrides, hook-derived local links and workspace-root link exemptions. An empty importer still detects newly added dependencies. Go cases are also evaluated through the unchanged Rust drift API in reference CI.

Native Berry fixtures begin with an LF lockfile on every OS, following Yarn’s existing-file line-ending preservation. Generated output is compared byte for byte without newline normalization.

Workspace freshness uses the root’s effective override set, resolves catalog and `$dependency` references, detects removed importers, and compares ignored-optionals and recorded runtime pins only in formats that retain resolution metadata. `devEngines` parsing remains a tolerant metadata view and performs no runtime installation or switching. Identity-scoped override and ignore selections are supplied per call.

Graph hashing ports the reference BLAKE3 serialization, cycle handling and edge resolution. Package identity can include patch and materialized-content fingerprints; build-dependent subtrees can include the selected engine. Whole-graph identity stays host independent and retains importer specifiers and dependency sections. Full graph comparisons also compare hashes against Rust. The format I/O layer uses these identities to retain unchanged files.

The format I/O layer keeps graph-equal lockfiles byte-for-byte unchanged, including comments, line endings and mtime. It checks resolved patch fingerprints and can enforce package-extension checksums before suppressing a write. Corrupt or strictly unparseable files take the normal write path. Legacy `lock.yaml` migrates to `nub.lock` on a real change; a duplicate legacy file is removed when the current file already exists. These helpers are not yet wired to install commands.

Native I/O tests retain Bun remote-tarball tuples and Berry custom archive qualifiers through an unchanged write, then remove installed packages and cache contents before a frozen install and a PATH Node resolution check. These exercise file preservation separately from the raw conversion writers' limitations above.

The shared lockfile read boundary rejects unsafe importer, package and dependency aliases before a graph reaches an installer. It also rejects registry-style dependency keys backed by local or remote source resolutions, retaining the reference's error codes and validation order.

Project I/O uses declaration-aware family precedence, keeps npm shrinkwrap priority, excludes Nub files during import and rejects unsupported binary Bun lockfiles. Reads retain the reference's fallback precedence on ambiguous declarations; writes refuse the ambiguity. An already-resolved branch selects `nub.<branch>.lock` or `pnpm-lock.<branch>.yaml`, with base-file fallback on reads. The future install session owns identity-scoped settings and Git branch resolution.

Resolver reuse indexes preserve dependency-path order, exclude bundled entries from reusable candidates, and distinguish vulnerability-filtered reuse from the first locked-version hint. Local source integrity matching retains Git commit/subpath rules. Advisory-aware version selection prefers acceptable dated versions, then undated safe versions, while still excluding known-too-new releases.

Resolver inputs retain the reference's prod/dev/optional/required-peer seeding priority. Catalog expansion rejects chained references and records authored ranges with their selected versions. Package extensions fill missing dependency and peer metadata without replacing declarations; local sources receive dependency additions only. The resolution driver combines these inputs with registry and local-source dispatch.

Semver comparison also targets the baseline Rust `node-semver` 2.2.0 crate. The resolver and lockfile consumers use its loose input grammar, partial comparator bounds and range-overlap behavior, while the independent npm `node-semver` tests retain the strict grammar. The test-only Rust probe compares accepted inputs, membership and range pairs. Dependency ranges normalize empty input to `*`; raw selectors and advisory ranges keep the reference's rejection.

Resolver override rules match direct-parent chains and Yarn ancestor wildcards, use range overlap for version-qualified targets, and rank named ancestors before target ranges. Alias version tails participate in matching. The parser is shared with lockfile drift checks, whose separate lower-bound matching behavior is retained; the Rust resolver's public rule API supplies the comparison oracle.

Task preprocessing expands catalogs before overrides, applies the reference's bounded alias rewrite loop, and keeps original and override-applied importer specifiers separate. It handles dependency removal, npm/JSR registry names, root-relative source overrides and named-registry routes. Built-in protocols retain priority over a colliding registry alias; the resolution driver fetches prepared tasks through their selected route.

Peer post-processing temporarily hoists required direct peers into importer scopes, retaining dependency-section classifications, then removes those entries before output. Required-peer diagnostics use the resulting dependency edges and suppress optional peers. Contextualization selects compatible providers by scope, handles cycles and nested peer suffixes, propagates descendant suffixes, and preserves distinct variants when shortened suffixes collide. Import conversion applies the pass to npm/shrinkwrap/Bun graphs. A test-only Rust comparison exercises the same graphs across workspace-root, dedupe and suffix-cap settings. The resolver driver applies this pass before returning its graph; install integration remains pending.

Trust helpers validate approval, trusted-publisher and provenance metadata shapes, compare evidence by publication order, retain prerelease and missing-time rules, and backtrack within the requested range after a refused pick. Package-version policies support exact names, name globs and version-range unions. Trust defaults are explicit; release-age exclusions start empty. The clock is provided by the execution context. These helpers do not verify attestation signatures and are not yet connected to an install handler.

Local-source preparation distinguishes importer-relative paths, parent-directory-relative transitives and project-root overrides. It enforces the reference exotic-subdependency policy and rejects generated-script paths that resolve outside the project. Metadata reads handle directory/link/portal targets and bounded tarball manifest scans without extracting files. Git sources use the separate commit-verified checkout and codeload caches described below.

The execution environment resolves tools from its own PATH and working directory and passes explicit streams and variables to child processes. Yarn `exec:` metadata generation runs a confined project script with PATH Node, provides the standard `execEnv` object and Node built-in globals, reads only the generated manifest, and removes its temporary directories on success or failure. Lifecycle/sandbox orchestration and generated-package materialization remain pending.

Remote-tarball resolution reads bounded package metadata through the registry transport, records a SHA-512 integrity pin, preserves hosted-Git archive identity, and honors offline policy. Diagnostics mask URL userinfo and recognized query credentials. The store separately validates and extracts the full archive during materialization.

Git source helpers resolve tags, branches, HEAD and abbreviated object IDs with the invocation's Git executable. Commit-verified checkouts are published atomically to the separate Go cache, with repository leases, stale-entry repair and offline reuse. This is source-resolution infrastructure; install command integration remains pending.

Hosted Git resolution rewrites transport to HTTPS, prefers commit-pinned codeload archives and preserves the original Git identity for subpath packages. Successful archives record SHA-512 integrity; a mismatched pin fails, while download or extraction errors fall back to Git. Codeload trees preserve safe symlinks on Unix and trigger clone fallback for symlinks on Windows. These paths have isolated archive and local-repository fixtures; they are not yet connected to the install command.

The resolution driver now connects registry version selection, local sources, workspace references, aliases, overrides, catalogs, lockfile reuse, package extensions, read-package callbacks and peer contextualization. It retains importer specifiers and skipped optional metadata, supports highest/time-based/lowest-direct modes, and emits packages to a caller callback. Live fixture graphs are compared with the unchanged Rust resolver. Primer metadata behavior, complete diagnostics and install-session integration remain in progress.

Exact optional versions use compact historical trust metadata without retaining every historical dependency tree. The resolver preserves newer exact releases when a stale full response omits them, refreshes partial metadata before range picks, and keeps prior cache files on failed refreshes. This flow is covered by exact-version, trust and repeated-resolution fixtures.

Metadata requests are prefetched when root, transitive and required-peer tasks are discovered. A bounded invocation scheduler deduplicates full and exact-version requests independently, merges completed responses on the BFS driver, and keeps graph and hook execution ordered. Optional fetch failures are retained without aborting unrelated dependencies; a successful exact response can supersede a failed full prefetch. Successful resolution drains speculative cache writes, while failure and cancellation stop and join outstanding workers. Adaptive concurrency tuning remains pending.

Registry host comparisons share WHATWG URL normalization between resolution and archive verification, including default ports, userinfo, IDNA, IPv4 and IPv6. Named-registry packages preserve their tarball URL when its host differs from the configured registry. The URL helpers have a test-only Rust oracle.

The registry fetch path now consumes resolved graph entries, verifies lockfile URLs against configured-registry metadata, checks archive integrity and package identity, and publishes content-addressed indexes. Alias and peer variants share the real package's index. Missing-integrity archives get computed SHA-512 bindings under the configured registry URL; warm/offline reads retain that registry separation. Store leases protect classification from maintenance, and per-package fetch leases prevent duplicate concurrent imports. Installation commands, Git prepare orchestration and batch materialization remain pending.

File and portal imports rescan their source directories, link sources produce no store index, and local tarballs use the bounded archive importer. Remote tarball imports enforce their source integrity pin independently of registry-store verification settings. Exec materialization shares the resolver’s confined PATH-Node generator runner, imports the complete generated tree, and removes temporary output after success or failure. Git prepare and lifecycle policies remain pending.

Batch fetching completes local imports before overlapping registry downloads and store imports. A serial result callback can start materialization as packages arrive; cached entries skip that callback. Loader and consumer failures cancel and join the remaining workers. Streamed canonical indexes are remapped to peer-contextualized package paths without losing Git/file source coordinates. The installation session still needs to supply its complete source and lifecycle policy.

The linker file layer creates fresh package trees from store indexes using copy, hardlink or platform reflink strategies, preserving executable bits and removing incomplete trees after failure. It probes store/project filesystem compatibility and distinguishes missing CAS files from destination errors. Windows copy and atomic-publication paths retry transient sharing failures. Virtual-store filenames retain the reference’s escaping, peer flattening, UTF-8 truncation and BLAKE3 suffix rules. Complete dependency layouts remain pending.

Directory links use relative symlinks on Unix and native NTFS junctions on Windows, without requiring Node, administrator privileges or Developer Mode. Replacing a stale link or empty slot leaves populated directories and link targets intact. Windows junction creation retries transient sharing failures and removes incomplete junction directories.

The executable linker creates relative POSIX symlinks by default, optional POSIX wrappers, and Windows cmd/PowerShell/Git Bash wrappers. It detects native binaries and safe shebang interpreters, preserves scoped bin paths and NODE_PATH entries, and skips wrappers whose name would recurse into their own interpreter. Bounded readers recover wrapper targets without executing them; recognizing a target alone does not establish ownership. Tests exercise relocation, PATH Node, native execution, permissions, foreign-directory preservation, and actual Rust writer/reader comparisons. Installer orchestration and bin ownership integration remain pending.

Bin declaration linking supports string/object `bin` fields and the `directories.bin` fallback, with deterministic collision order and directory containment checks. A pre-build pass can capture exact launcher bytes and symlink targets. Relinking then preserves lifecycle replacements or deletions as complete command families, including every Windows launcher; declarations removed after a build have their previously managed paths cleaned up. Complete install/lifecycle orchestration remains pending.

The graph-driven bin pass links each importer’s direct dependencies, workspace/link/portal targets, bundled binaries and self-bins. It uses on-disk manifests rather than stale lockfile bin metadata. Hoisted placements run before direct dependencies and self-bins; every concrete placement gets its own targets. Isolated per-dependency bins are created only when approval policy or the trust floor permits builds. The pass supports pre/post-lifecycle snapshots and skips virtual workspace importers and virtual-store-only installs. Package layout and CLI install orchestration remain pending.

Directory-bin containment on Windows resolves the opened file’s final path through junctions, including long paths; it does not rely on Go’s symlink-only path walker. This keeps a junction to an external directory from being treated as a package-local bin directory.

The virtual-store materializer stages complete package entries before atomic publication, with bounded transient rename retries and cleanup on failure or cancellation. Scoped sibling links, nested local targets, graph-hashed identities and warm dependency-link repairs use the reference’s path conventions; Windows junctions target the final location before the staging rename. Tests cover real Node resolution, concurrent publication, failed-CAS cleanup and cold/warm Rust tree snapshots. Quarantine processing, complete project layout orchestration, store-state reuse and the install CLI remain pending.

Materialization applies git/plain unified patches before publishing an entry. The applier follows the reference’s bounded 20-line context search, trailing-whitespace tolerance, multi-file boundaries, quoted paths, CRLF preservation and final-newline pragmas. Deletions remove files, and atomic writes break CAS hardlinks before changing content. Every target component is checked for symlinks or Windows reparse points. Tests compare resulting files and errors with the unchanged Rust linker.

Applied-patch tracking retains the existing `.nub-applied-patches.json` project format. Fingerprints normalize CRLF before SHA-256 hashing. Reconciliation removes project-local entries for changed or removed selectors, including aliases and peer contexts, while unchanged packages remain reusable. Tracking writes publish atomically and report failures; removing the last patch removes the sidecar. These helpers still need the install driver and patch commands.

On macOS, materialization removes only `com.apple.quarantine` from indexed executables and `.node`, `.dylib`, and `.so` files after patches and on indexed cache hits. Build-cache restores can use a separate tree walk that skips symlinks. Attribute failures retain the install and emit at most one warning per invocation; other attributes remain untouched. Native macOS tests cover actual attributes, cold/warm entries and symlink containment. Other operating systems perform no quarantine traversal.

The isolated linker now connects package materialization, patch reconciliation, root and workspace links, mutable directory refresh, public/hidden hoisting, workspace deduplication and virtual-store-only behavior. Hidden hoisting reserves root versions before choosing the shallowest remaining package; public hoisting retains the reference's sorted first-winner rules. Cleanup preserves dotfiles and custom virtual-store leaves, reclaims abandoned staging trees, and validates modules-directory containment before touching entries. Tests exercise actual Node resolution and compare cold/warm project trees and counts against Rust.

Shared global linking places registry and pinned source packages under graph-hashed paths, with absolute project entry links. Selected packages can remain real project-local directories; disk materialization also retains their shared copies for store-resident dependents. Only project-local hidden trees carry unversioned aliases, and a full hidden hoist selects the project-local layout. Hash changes retarget entries without modifying old shared contents.

The hoisted linker plans real package directories with root-direct version priority, consumer-count tie-breaking, nested conflicts, all three hoisting limits and shared workspace roots. Members outside the root keep their own resolvable trees. Parents finish before deeper placements replace bundled files. Driver-vouched complete entries preserve build output, and the placement map records every installed copy for bins and scripts. Tests compare cold/warm file trees, counts and placement maps with Rust. Automatic phantom detection, complete install-state reuse and CLI installation remain pending.

PnP installation is an explicit refusal in the recorded Rust baseline, despite older design notes describing a planned writer. The Go install-policy helper preserves that refusal: Yarn Berry defaults to PnP; `YARN_NODE_LINKER`, home configuration and ancestor/member `.yarnrc.yml` determine the effective value. Read-only commands and other PM identities bypass this check. Explicit engine `node-linker=pnp` values also fail. ZIP/PnP generation is therefore not an implemented baseline feature to port; using an existing project's PnP loader for PATH-Node scripts remains a separate execution concern. CLI wiring of the guard is pending.

Install freshness inputs include manifest install-shape digests and local directory content/metadata fingerprints. Documentation and script-only manifest edits retain the shape; dependency, workspace and build-policy edits change it. Local fingerprints skip symlinks, `.git` and `node_modules`, retain executable bits, and capture nanosecond timestamps before reading content. Rust probes compare the digest bytes. The command integration remains pending.

`internal/installstate` reads and writes the existing `.nub-state` JSON sidecars with separate freshness, placement and license records. It preserves unknown versus completed dependency builds, migrates legacy state, tracks interrupted linking and validates root/workspace entries, package identities and shared-store edge targets. Hoisted snapshots record the ancestor placement visible to each member. The installer command pipeline remains pending.

The state differential probe compiles the unchanged private Rust state declarations and filesystem verification functions extracted at test build time. Its corpus compares compact state/freshness bytes, malformed-input acceptance and installed-layout reasons; it is never linked into the Go executable.

State recording captures root/member lockfiles, manifest hashes and install shapes, copied local sources, dependency-build completion, layout and license data after a successful install. Freshness checks retain the reference reason order and distinguish stable build denials from builds still owed. Metadata-only source or lockfile touches refresh the small sidecar; an actual content, workspace or policy change requires installation. Hoisted reuse requires matching content fingerprints and a completed prior link phase. Settings digest generation and command orchestration remain pending.

`internal/installdelta` computes alias-aware package content hashes, multiset graph digests, added/removed/changed entries and cycle-safe subtree hashes. Dependency builds are grouped into children-first phases through non-building bridges; unrelated graph depth does not delay an independent build. Generator content is read only through the existing project-containment guard. These helpers feed future install and lifecycle consumers.
