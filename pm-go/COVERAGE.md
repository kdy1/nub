# Go PM coverage

Reference: `2a4573ef059798b75f789aa8c22da51e470e3138`. This records current implementation coverage, not a compatibility claim. An internal component or a passing fixture does not imply that its command family is implemented.

## Command boundary

The command registry and aliases are extracted from the Rust adapter and checked by `internal/surface`. `arguments.json` snapshots 65 argument declarations with 309 fields, including flatten relationships and source attributes. Adapter rewrites, manual subcommand grammars, accepted option combinations, and the command-by-command differential suite remain incomplete.

| Command | Aliases | Go behavior |
| --- | --- | --- |
| `install` | `i` | No handler; explicit port-incomplete error |
| `ci` | — | No handler; explicit port-incomplete error |
| `pm` | — | No handler; explicit port-incomplete error |
| `add` | `a` | No handler; explicit port-incomplete error |
| `remove` | `rm`, `uninstall`, `un`, `uni` | No handler; explicit port-incomplete error |
| `update` | `up` | No handler; explicit port-incomplete error |
| `import` | — | No handler; explicit port-incomplete error |
| `dedupe` | — | No handler; explicit port-incomplete error |
| `prune` | — | No handler; explicit port-incomplete error |
| `rebuild` | `rb` | No handler; explicit port-incomplete error |
| `fetch` | — | No handler; explicit port-incomplete error |
| `link` | `ln` | No handler; explicit port-incomplete error |
| `unlink` | `dislink` | No handler; explicit port-incomplete error |
| `approve-builds` | — | No handler; explicit port-incomplete error |
| `ignored-builds` | — | No handler; explicit port-incomplete error |
| `patch` | — | No handler; explicit port-incomplete error |
| `patch-commit` | — | No handler; explicit port-incomplete error |
| `patch-remove` | — | No handler; explicit port-incomplete error |
| `clean` | — | Reference refusal reproduced; complete output/argument parity not yet established |
| `purge` | — | Reference refusal reproduced; complete output/argument parity not yet established |
| `deploy` | — | Reference refusal reproduced; complete output/argument parity not yet established |
| `dlx` | `x` | No handler; explicit port-incomplete error |
| `create` | — | No handler; explicit port-incomplete error |
| `recursive` | `multi`, `m` | Reference refusal reproduced; complete output/argument parity not yet established |
| `list` | `ls` | No handler; explicit port-incomplete error |
| `la` | — | No handler; explicit port-incomplete error |
| `ll` | — | No handler; explicit port-incomplete error |
| `why` | `w` | No handler; explicit port-incomplete error |
| `outdated` | — | No handler; explicit port-incomplete error |
| `audit` | — | No handler; explicit port-incomplete error |
| `licenses` | — | No handler; explicit port-incomplete error |
| `deprecations` | — | No handler; explicit port-incomplete error |
| `peers` | — | No handler; explicit port-incomplete error |
| `query` | — | No handler; explicit port-incomplete error |
| `check` | — | No handler; explicit port-incomplete error |
| `bin` | — | No handler; explicit port-incomplete error |
| `root` | — | No handler; explicit port-incomplete error |
| `sbom` | — | Reference refusal reproduced; complete output/argument parity not yet established |
| `view` | `info`, `show`, `v` | No handler; explicit port-incomplete error |
| `search` | — | No handler; explicit port-incomplete error |
| `publish` | — | No handler; explicit port-incomplete error |
| `pack` | — | No handler; explicit port-incomplete error |
| `version` | — | No handler; explicit port-incomplete error |
| `deprecate` | — | No handler; explicit port-incomplete error |
| `undeprecate` | — | No handler; explicit port-incomplete error |
| `dist-tag` | `dist-tags` | No handler; explicit port-incomplete error |
| `unpublish` | — | No handler; explicit port-incomplete error |
| `login` | `adduser` | No handler; explicit port-incomplete error |
| `logout` | — | No handler; explicit port-incomplete error |
| `whoami` | — | No handler; explicit port-incomplete error |
| `owner` | `owners` | No handler; explicit port-incomplete error |
| `token` | — | No handler; explicit port-incomplete error |
| `store` | — | No handler; explicit port-incomplete error |
| `cache` | — | No handler; explicit port-incomplete error |
| `cat-file` | — | No handler; explicit port-incomplete error |
| `cat-index` | — | No handler; explicit port-incomplete error |
| `find-hash` | — | No handler; explicit port-incomplete error |
| `config` | `c` | No handler; explicit port-incomplete error |
| `get` | — | No handler; explicit port-incomplete error |
| `set` | — | No handler; explicit port-incomplete error |
| `pkg` | — | get/set/delete/fix; manifest differential fixtures pass; full CLI parity incomplete |
| `set-script` | `ss` | Manifest editor and variadic arguments; differential fixtures pass; full CLI parity incomplete |

## Internal components

| Area | Implemented and tested | Not implemented or not yet verified |
| --- | --- | --- |
| Context and parser | Explicit directory, environment, streams and per-invocation tool lookup; manifest option/variadic parsing | Full pnpm grammar, option-position and exit/output parity |
| Project and config | PM identity/major override rules, source-scoped npmrc/auth/TLS/proxies, ordered manifests, install-shape digests, workspace glob matching, Nub shared-store/hoist materialization selection and contradiction checks; checked 152-setting catalog, invocation-owned scalar/list resolution, source precedence and managed hardening; native install-block validation, field overlays and identity-scoped lowering | Settings file/session assembly, complete nub.jsonc loading, Yarn/Bun adapters, workspace discovery and filters |
| Resolution | npm and baseline Rust semver oracles, version/publish-age selection, source identities, platforms, graph traversal and optional/peer passes, direct override matching and nested-object flattening, locked reuse and advisory preferences, catalog/override/alias preprocessing, ancestor override rules, named registry routing, package extensions, direct dependency seeding, temporary peer hoists, unmet-peer diagnostics, fixed-point peer contexts, nested/cyclic scopes, ancestor suffix propagation and dedupe collision handling; package-version exclusion policies, trust evidence/history checks and bounded repicks; local path rebasing/anchors, exotic-transitive policy, bounded manifest reads, generator path containment and PATH-Node metadata generation, remote-tarball metadata and integrity pinning, hosted Git/subpath metadata with integrity-pinned codeload and Git clone fallback; connected BFS driver with workspace/source/registry dispatch, lockfile reuse, time cutoffs, hooks, optional policy and peer finalization, exact optional metadata and stale/full history merging, bounded concurrent prefetch with ordered graph mutation and cancellation | Adaptive fetch tuning, primer metadata behavior and complete diagnostics; install conflict integration and generated-package materialization |
| Lockfiles | npm v1/v2/v3/versionless reader and v3 writer; canonical hoist layout, graph identity/content hashes and patch selector resolution; native npm fixture round trips; pnpm peer/patch/alias keys, checksums, typed YAML document selection and reference subset dispatch, v9+ reader/writer and output layout rules; Bun text v1/v2 graph reader, source policy and v1 writer; Yarn classic tokenizer/graph reader, workspace discovery and conversion writer; Berry graph reader/writer, resolution/patch descriptor matching and YAML output layout; patch/catalog/extension, importer and workspace metadata freshness checks; unchanged-file I/O guard, alias/source validation, declaration and branch filename selection, and legacy Nub filename migration | Broader native acceptance matrix; settings-driven branch resolution and frozen/install orchestration |
| Network and store | HTTP/TLS/auth/redirect limits, metadata caching/offline, SRI, safe archives, BLAKE3 CAS/indexes, process locks, directory/tarball imports, Git ref resolution and commit-verified clone caching with offline reuse, atomic codeload trees and integrity sidecars; registry graph-to-store fetching, frozen URL checks, integrity/content policy, URL-keyed computed-integrity bindings, alias deduplication and offline/corrupt-cache recovery, file/portal/tarball and generated-tree imports; local directory content and metadata fingerprints; bounded batch fetching with cancellation, result streaming and peer-index remapping | Git prepare, adaptive transfer scheduling and streaming archive import, maintenance commands, complete installation-state orchestration |
| Linkers | Graph and placement prerequisites; virtual-store filename encoding, fresh file materialization with copy/hardlink/reflink strategies, executable modes and failure cleanup, Unix directory symlinks and native Windows junctions; relative executable symlinks and POSIX/cmd/PowerShell/Git Bash wrappers, interpreter/native detection, NODE_PATH and bounded wrapper readers; manifest/directory bin linking and lifecycle replacement tracking; graph-driven root/workspace/bundled/per-dependency passes and ordered hoisted placement bins; atomic virtual-store entries with scoped/nested dependency links and warm repairs; git/plain unified patch application with bounded context search, CAS isolation and path containment; applied-patch state encoding and project entry invalidation; indexed and tree-based macOS quarantine removal with bounded diagnostics; isolated root/workspace layouts, local refresh, patch state, public/hidden hoist, deduplication and cleanup; graph-hashed shared global entries, pinned sources and selected real project-local copies; hoisted root/workspace placement, limits, conflict priority, depth-ordered replacement and vouched reuse | Automatic phantom selection, bin ownership integration, phantom dependencies, reuse |
| Installation state | Existing state/freshness/placement/license sidecar formats, strict typed reads and legacy migration, atomic writes, link-in-progress sentinel, captured isolated/hoisted layout and shared-edge verification; successful-install recording, manifest/lockfile/local-source freshness, workspace membership and build-completion checks, metadata-only refresh and hoisted reuse gates; package content and multiset graph hashes, delta/subtree invalidation and dependency-build phases | Resolved settings hashes and CLI reuse integration |
| Scripts | No lifecycle execution handler | Build approvals, ordering, sandbox, environment, exit propagation, node-gyp bootstrap |
| Remaining commands | Verb and alias registry | Queries/audit, pack/publish/auth, configuration/cache/PM management, dlx/create |

## Validation boundary

- CI runs Go tests with the race detector, vet, and cgo-disabled builds on Linux, macOS, and Windows.
- Rust differential tests cover manifest commands, npm library serialization, and pnpm graph parsing and writer bytes, Bun and Yarn graphs/writer bytes under strict/lenient source policies, graph hashes, drift checks, install-input fingerprints, state/freshness serialization and layout checks, semver ranges, ancestor override rules, peer hoist/removal diagnostics, contextualized peer graphs, package-version policies, and trust evidence/diagnostics. A test-only probe uses the unchanged Rust libraries; production Go never loads it. These cases do not establish install or full PM parity.
- Pinned npm, pnpm, Bun, and Yarn oracles exercise lockfile round trips and clean/frozen installs against an isolated registry. It does not run the Go installer, which has no handler yet.
- A workspace conflict fixture retains the reference writer's redundant member-local hoist. Its difference from native npm is asserted explicitly, without normalizing it out of Rust/Go byte comparisons. The internal I/O guard preserves graph-equal files; install command integration is still unimplemented.
- Bun reference serialization sorts nested package keys lexically and writes an empty registry slot for remote tarballs, unlike native Bun. The native oracle asserts those exact differences; Rust/Go output comparisons remain byte-exact. Bun 1.3.14 rejects the reference remote-tarball tuple during frozen install; this is a negative compatibility test, not an accepted-lockfile claim. The I/O guard retains the native remote-tarball file and passes a cold native frozen install. Install command integration remains pending.
- Node-semver 7.7.4 is an independent range oracle. The baseline Rust engine uses node-semver 2.2.0; its loose grammar, membership and intersection behavior have a separate differential corpus. Resolver and lockfile consumers use the baseline semantics, implemented in Go.
- Full Rust regressions, interruption/retry/concurrent-install scenarios, every incumbent lockfile acceptance matrix, and performance measurements remain unverified.

## Deliberate exclusions

Nub runtime augmentation, automatic TypeScript transformation, Node provisioning, and Nub self-version delegation are outside this executable. Scripts and fetched tools use PATH Node. Rust sources and the default Nub PM path are retained. Go global state uses the separate `nub-pm-go` namespace. Production code does not invoke the Rust engine.

PnP installation is rejected by the recorded Rust baseline (`pm_engine::pnp_fatal_if_requested` and the engine linker selector). `internal/installconfig` ports its Yarn-identity and mutation gates, environment/home/ancestor precedence and explicit linker errors; install-command integration remains pending. Existing-loader execution is a separate script capability.
