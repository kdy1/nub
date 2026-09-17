# Architecture

Nub is a Rust CLI that augments the user's installed Node. It ships no runtime, patches no Node source, and embeds no `libnode`.

Everything Nub adds reaches the process through a mechanism Node already publishes:

| Surface | Carries |
| --- | --- |
| Preload injection | One entry file that registers everything below |
| Module hooks | TypeScript, JSX, path aliases, extensionless imports, data formats |
| Flag injection | Experimental features the installed Node has but keeps gated |
| Environment | Files read before the spawn, prepended to the child |
| Native addon | The transpiler, the TypeScript resolver, the data parsers |
| Path shim | A `node` that resolves back to Nub, so augmentation survives a subprocess |

> [!NOTE]
> The test that decides whether a feature is in scope: would a user on plain Node, plus the corresponding `module.register()` call, preload, or addon, get the same result? If not, the feature needs a different mechanism or it is dropped.

The choice of per-file hooks over a bundler pass is in [[research/augmentation-layers]].

## Feature support across Node versions

Nub supports Node 18.19 and above. Across that range a feature may be native, gated behind a flag, or absent — so making it work means a different action per version.

All of it lives in one table, 48 features deep. Each carries sorted, non-overlapping version bands, and each band names exactly one mitigation:

| Mitigation | What Nub does |
| --- | --- |
| Native | Nothing. The version already ships it. |
| Unflag | Injects the experimental flag, across the exact range where it exists and is still required |
| Polyfill | Installs a JavaScript polyfill, guarded by a `typeof` feature detect |
| Storage file | Passes a workspace-keyed `--localstorage-file` path, for Web Storage only |
| Unflag on argv | Injects a V8 flag Node accepts only on the command line, never through `NODE_OPTIONS` |
| Runtime V8 flag | Turns a V8 flag on from inside the process, the first time a module that uses its syntax is loaded |

Thirteen distinct flags are injected this way. They gate builtin modules (`node:sqlite`, `node:ffi`, `node:vfs`, `node:stream/iter`), web globals (EventSource, WebSocket, Web Storage), module kinds (vm, wasm, addon, text import), module-syntax detection, and one performance path: `AsyncLocalStorage` on V8 context frames, Node 24's default, on the 22 and 23 lines. A fourteenth, `--js-defer-import-eval` for `import defer`, never rides the command line at all. Node refuses it in `NODE_OPTIONS` by name, and a V8 flag that is non-default at startup makes Node reject its embedded code cache for every internal module compiled afterwards, which cost every program on Node 26.4+ several milliseconds while the flag rode argv. So the preload turns it on with `v8.setFlagsFromString` the first time it loads a module whose source uses the syntax, which V8 honors because it reads that flag only in the parser, and a program that never uses the syntax runs with V8's default flags. The polyfilled set is web and TC39 globals: Temporal, URLPattern, Worker, `navigator`, Float16Array, the disposable stack types, and the iterator, promise and collection helpers.

Below a feature's floor no band matches and Nub does nothing — the feature is unavailable rather than half-present.

Banding is exact because it has to be. Injecting an experimental flag on a version that does not have it is a hard startup abort, not a warning. Several rows carry two disjoint bands where a backport reached one release line and not another; `node:sqlite` is the clearest case, having been unflagged, re-flagged, and unflagged again. ShadowRealm is never injected at all, because the flag crashes embedded Node through a snapshot hash mismatch. That hazard generalizes, and it is why no version-gated flag rides `NODE_OPTIONS`. That string is inherited by every process below, including an embedded Node booting from a V8 snapshot, and the set Nub builds is matched to the version of the Node it resolved — so a descendant running an older Node meets a flag it cannot parse and aborts at startup. Every injected flag travels on argv instead, reaching the process Nub spawns and nothing beneath it; a child `node` gets its own copy by re-entering Nub through the PATH shim. What stays in `NODE_OPTIONS` is the preload, which every Node accepts, and flags whose floor sits at or below Nub's own support floor.

The flag-injection logic in [[crates/nub-core/src/node/flags.rs#compute_inject_flags]] reads the table in [[crates/nub-core/src/node/feature_matrix.rs#FEATURES]] rather than keeping its own copy, so a version-gated claim traces to a row. A band that runs to infinity would eventually inject a flag the running Node has dropped, so both unflag shapes are checked against the real binary before injection, by different probes. The ordinary set is filtered against the list Node reports as accepted in `NODE_OPTIONS` — a cheap read that stays a valid existence check even though the flags themselves travel on argv. The argv-only and runtime sets cannot use that list, since a flag Node refuses there is absent from it by construction, so each is checked by spawning the binary with it once and caching the verdict. A flag the binary no longer takes is dropped rather than aborting it at startup. Surveys behind the bands: [[research/node-experimental-flag-lifecycle]], [[research/node-flag-arity]].

## Two tiers

The runtime exists in two shapes, chosen by the availability of the synchronous hooks API and carried as [[crates/nub-core/src/node/version.rs#SupportTier]]. The floor of 18.19 is set by what the extension mechanisms permit.

| | Fast tier | Compat tier |
| --- | --- | --- |
| Node | 22.15 and above, except 23.0 to 23.4 | 18.19 to 22.14, and 23.0 to 23.4 |
| Preload channel | `--require` | `--import` |
| Hooks | `module.registerHooks`, synchronous, in-thread | `module.register`, in a loader worker |
| Polyfills | Lazy getters | Eager import |

The 23.x exclusion is not a special case, it is the tier definition applied correctly. `module.registerHooks` is a semver-minor that reached the 23.x Current line at 23.5.0 and the 22.x LTS line only later, at 22.15.0, so 23.0 to 23.4 sorts above the 22.x floor while carrying no synchronous hooks API. A version comparison against 22.15 alone therefore claims a capability those releases do not have.

Using `--require` on the fast tier is a correctness mechanism, not an optimization. An `--import` preload forces eager ESM loader initialization, which routes even a CommonJS entry point through the async module job and breaks `executionAsyncId`, sync exception origin, `require.main.id` and `module.parent`. Coverage and composition behavior of the hooks API is measured in [[research/registerhooks-coverage-matrix]].

The standalone runner also accepts `--import @nubjs/runner`. Its own preload is excluded from foreign-loader detection, while additional loader flags and runtime hook registrations retain the composition guards. Earlier foreign `--require` preloads conservatively disable the CommonJS cache repair because they may register hooks before detection starts. When it is the only loader, imported CommonJS dependencies retain their `require.cache`, `require.extensions` and `require.resolve.paths` APIs.

## TypeScript and resolution

Both ride the same hook pair, and both run in Rust behind a single call across the addon boundary.

The load hook handles type stripping, the non-erasable syntax other strippers refuse (enums, parameter properties, `namespace`, `import =`), JSX, legacy decorators with metadata emission, down-levelling of `using` and the RegExp `v` flag, and the YAML, TOML, JSON5 and JSONC loaders. Output is content-addressed on disk with the source map already inlined, so a cache hit does no work in JavaScript. Stage 3 decorators are not transformed; the runtime raises a diagnostic rather than emitting wrong code.

The resolve hook is additive only. It layers tsconfig path aliases, extensionless probing for TypeScript extensions, and Yarn Plug'n'Play reads on top of Node's own resolver, and returns nothing when it has no additive answer — at which point resolution falls straight through. There is no reimplementation of Node's resolution algorithm anywhere in Nub, which confines the risk to what Nub adds. That is validated by running Node's own resolution test subset twice, once in passthrough and once augmented, and asserting parity: [[research/resolution-conformance]].

When JavaScript syntax inspection identifies a required transform, the transform reuses the inspected bytes instead of reading the file again. Loader execution order remains unchanged on both tiers.

Background: [[research/tsgo-vs-oxc-for-transpile]], [[research/wasm-vs-napi-for-transpile]], [[research/emit-decorator-metadata]], [[research/tsconfig-paths]], [[research/ts-extension-precedence]].

## Composition

Real toolchains shell out. If augmentation stopped at the first process, TypeScript would work in an entry point and fail in everything it launched.

So Nub writes a private `node` into a temporary directory named with [[crates/nub-core/src/node/spawn.rs#PATH_SHIM_PREFIX]] and puts it first on `PATH` for the subtree it started. A child that spawns `node` lands back in Nub and gets the same treatment. The directory is per-invocation, owner-only, reclaimed on exit, and swept by a background reaper for runs that were killed. The argv contract that shim honors is in [[research/node-flag-hijack-compat]]; the general technique is surveyed in [[research/node-impersonation]].

The persistent shim installed by `nub node shim` is the opposite: it runs the resolved Node unaugmented. Version management is its job. A globally augmenting `node` would load environment files and inject globals into every Node process on the machine.

## Turning it off

Both `--node` and a truthy `NODE_COMPAT` disable runtime augmentation — no hooks, no preload, no injected flags, no path shim. They compose, and `NODE_COMPAT` is stamped tree-wide so every descendant inherits it.

Two details make the switch trustworthy. Compat mode does not merely skip augmentation; it restores a parent's augmented environment to its pre-Nub state. And version provisioning stays on, because running on stock Node and running on no particular Node are different requests.

## Main-heap memory tuning

Direct Node launches on Linux x64 use a small semi-space floor only in a measured, closed set of Node releases and cgroup budgets. File runs and Node-backed `exec`/`nubx` binaries share this launch path.

The policy in [[crates/nub-core/src/node/gc.rs#eligible]] requires at least 512 MiB after accounting for ancestors and physical memory. The leaf budget must also be within the release-specific range below; a tighter parent alone cannot enable an override of an already-large nursery.

| Node release | Eligible leaf budget, inclusive | Default semi-space in that range |
|---|---|---|
| 22.23.2 | 512 MiB–1 GiB | 1–4 MiB |
| 24.20.0 | 512 MiB | 1 MiB |

Node 24's own nursery reaches 16 MiB immediately above its upper bound. Node 22 above 1 GiB and Node 26 retain their defaults because production-mode SSR regressed with the larger nursery, despite gains in retained-object workloads. Smaller budgets retain Node's defaults because the larger nursery can increase cgroup OOM kills under allocation pressure. Explicit startup options, PnP, environment-owner loaders, compatibility mode, and inherited augmented processes disable it. Watch and compiled launchers do not apply this policy.

The launcher supplies `--max-semi-space-size=16` for main-isolate initialization. Before any application preload or entry code runs, the fast CJS preload resets the process-global flag to zero. V8 has already stored main's limit, while later Worker isolates can still apply their own `resourceLimits`. Keeping the global override would silently replace explicit Worker young-generation limits, even with an empty `execArgv`.

This is a one-shot, release-specific startup operation, not a live GC controller. Adding a release requires auditing V8's flag readers and Node's preload ordering, then running constrained-memory and Worker/fork acceptance tests. The startup helper also rides argv so Workers with a replacement environment still run its argument cleanup. Both injected arguments are hidden from `process.execArgv`; application-created child processes do not inherit them as explicit heap settings. User heap flags remain visible and unchanged.

## Environment files

Four files load in precedence order, with the real process environment always winning:

- `.env.<mode>.local`
- `.env.local`
- `.env.<mode>`
- `.env`

Loading happens in the CLI before the spawn rather than in a preload. Cross-runtime load order, the expansion subset, and the security case for the ordering are in [[research/env-file-loading]].

## How the code is laid out

Three Cargo workspaces, and the splits are structural rather than organizational.

| Workspace | Holds | Why it is separate |
| --- | --- | --- |
| Root | The CLI, Node discovery and spawn, version management, workspace handling | — |
| Native addon | The transpiler and parsers, as a cdylib | The panic strategy is profile-global, and a library loaded into the user's Node must unwind rather than abort |
| Package manager | The install engine, vendored | Its own workspace root |

The JavaScript runtime — preloads, hooks, polyfills, worker shims — is compressed into the binary at build time and inflated once into a per-user cache directory, verified against digests baked into the executable. Extraction publishes by rename, so a concurrent reader sees a complete directory or none.

The full transformer links into the addon only. The CLI binary carries a parser subset and never the transformer.

## Separate Go package manager

An experimental Go executable lives in `pm-go`. It has its own module and command inventory; it does not change Rust Nub's dispatch or use the Rust engine as a fallback.

The inventory is checked against the Rust PM registry. Commands without a Go handler exit with an explicit error. The comparison harness records exit status, both output streams, file bytes, permissions, and symlinks in isolated project fixtures.

The Go manifest editor implements `pkg get/set/delete/fix` and `set-script`. It retains property order and the source file's JSON style, and publishes edits by rename only after every requested change succeeds.

The Go registry configuration layer tags each npmrc source. Unscoped credentials bind to the registry declared by the same source. URI and package scope determine credential selection; project files cannot set credential helpers or proxies, expand environment secrets, or disable TLS validation. File discovery and environment lookup take an explicit execution context.

The Go typed settings resolver takes invocation-owned source bags and applies the reference CLI, environment, file and managed-policy rules. Pnpm-named inputs are identity gated; workspace YAML layout fields are excluded under Nub. Its checked setting catalog and differential probe track the original engine accessors. Complete settings-file and install-session assembly remains in progress.

The settings npmrc view splits the shared loader's entries into user and project tiers and applies pnpm 11's key policy without filtering registry authentication. Nub's unsupported engine settings are excluded across aliases and source tiers.

The Go native install-block validator preserves strategy-specific linker options, field replacement, additive ejection patterns and release-age rounding. Its lowering step applies layout across identities while limiting native resolution fields to Nub-owned projects. The enclosing JSONC loader is still separate work.

Go layout configuration now applies resolved settings to the isolated and hoisted linker plans. Default settings inspect the supplied root and workspace manifests for store compatibility, retaining the reference's injected-dependency and framework-version gates. Session discovery and full install orchestration remain in progress.

Ordered Go JSON values retain 64-bit signed and unsigned integers. Floating-point parsing and formatting follow the reference number model, including negative zero, exponent notation, and out-of-range rejection. Manifest differential tests cover these boundaries.

The Go semver layer expands npm ranges into comparator sets and admits prereleases only for explicitly selected version tuples. Its CI tests use the pinned node-semver fixture as an independent oracle.

Version selection retains lockfile and dist-tag preferences before scanning candidates. It distinguishes unsatisfied ranges from releases that are too new or have no provable publish age. A blocked stable `latest` tag can fall back only at or below its tagged version; protocol selectors cannot resolve through registry tags.

Go registry requests enforce response-size, total-time, and idle-time limits. Retry budgets distinguish temporary status codes from timeouts. Credentials stay stripped after a redirect crosses authorities, and HTTPS redirects cannot downgrade to HTTP. TLS roots, proxies, and credential-helper environments come from the captured execution context.

Registry metadata caches partition by registry URL and response format. Fresh entries avoid a request; stale entries use conditional requests unless offline policy selects the cached copy. Failed or malformed responses leave the previous cache entry intact. Concurrent readers share a fetch and receive separate parsed metadata objects.

Go store integrity verification selects the strongest supported SRI algorithm and accepts any matching digest within that algorithm. Package-content validation checks the tarball manifest's name and version, preserving the reference's normalization for leading `v`, build metadata, and non-registry locators.

Tarball extraction runs inside a newly created filesystem root. It strips the wrapper component, rejects traversal and non-regular entries, validates gzip completion, and enforces the reference archive limits. Failed extraction removes its partial tree; existing destinations are never merged or overwritten.

Dependency helpers distinguish workspace aliases, relative locators, and version ranges. Platform matching uses npm OS/CPU/libc names and supports additional target architectures. Linux libc detection examines the running loader before installed loader files so cross-compilation tooling does not select the wrong optional packages.

Go cache and store defaults retain the reference platform precedence under the separate `nub-pm-go` namespace. Advisory file leases coordinate processes, support shared readers and exclusive maintenance, and allow cancellation while waiting. Lock files remain in place after release so concurrent processes always lock the same inode.

The Go CAS streams files into temporary storage, computes BLAKE3 keys, and publishes complete files under shard locks. Executability lives in package indexes while shared CAS files remain non-executable. Index keys include tarball integrity; read-only fallback stores are never modified. Missing or truncated files invalidate a cache read.

Tarball and local-directory imports now produce Go CAS indexes. Local imports skip `.git`, `node_modules`, and symlinks. HTTP tarball requests match authentication against the full tarball URL, request identity content encoding, apply download limits, and refuse network access in offline mode.

The Go lockfile source model distinguishes local directories, archives, links, portals, generators, Git repositories, and remote tarballs. Source keys hash normalized identities, including Git commits and subdirectories. Graph-edge lookup accepts full keys, version tails, and the hashed keys used for pinned remote sources.

The Go dependency graph preserves lockfile metadata across structural filters. Reachability and shortest-depth walks handle cycles and all importer roots. Platform filtering removes optional and bundled edges before collecting unreachable packages; required paths remain. Peer propagation excludes injected peer edges and a package’s own peers.

The install-facing Go manifest parser tolerates legacy dependency, script, and engine shapes while validating declared names, workspace patterns, catalogs, and bundled dependencies. Workspace objects preserve authored-empty fields for lockfile serialization. Neutral dependency metadata retains optional-peer and build-denial semantics.

Workspace member matching preserves the reference’s distinct rules for ordinary positive globs, recursive positive globs, and exclusions. Brace expansion, case sensitivity, directory boundaries, and `node_modules` exclusion are covered by Go tests.

The Go npm lockfile reader lifts legacy nested shrinkwraps into the common graph and reads v2/v3 install-path maps directly. Nested lookup preserves dependency and peer placement, including parent workspace directories and pointer links. Root workspace patterns filter member importers; local links retain their own source identities. Metadata and declared ranges survive parsing, while incomplete legacy graphs produce the reference diagnostic.

The shared Go lockfile layout pass collapses peer contexts into canonical package identities and builds a deterministic hoist tree. A conflicting nearest ancestor forces nesting. Existing root placements survive only while reachable, and explicit direct dependencies keep their slots. Separate reachability passes distinguish dev, optional, and peer-only paths for lockfile flags.

The Go npm writer emits v3 with npm’s scalar/object key ordering, declared ranges, source URLs, and reachability flags. Fresh workspace identities come from member manifests; existing workspace and local link conflicts retain separate placements. Native npm fixtures exercise byte-preserving round trips. Publication is atomic and respects the process umask.

Go pnpm key helpers preserve nested peer suffixes, strip write-time patch markers, and record transitive alias remaps. Registry-qualified versions remain distinguishable from reserved source protocols. Malformed peer suffixes retain the reference diagnostic behavior.

Go pnpm checksum helpers reproduce the reference’s unordered object/array hash stream, UTF-16 string lengths, and number notation. Local pnpmfile hashes normalize CRLF and combine files in sorted path order. Callers supply the hook paths explicitly, so checksum calculation does not discover pnpm config in another PM’s project.

The Go materializer applies package patches before publishing virtual-store entries. Applied-patch state uses Nub's existing project sidecar and CRLF-normalized SHA-256 fingerprints. Changed selectors invalidate project-local alias and peer placements; global identities carry their patch fingerprints in graph hashes. Installation orchestration remains separate work.

On macOS, Go package materialization strips only the quarantine attribute from indexed executables and native modules, including indexed cache hits. A separate walk handles restored build output without following symlinks. Failures produce a bounded warning and leave other attributes intact.

The Go isolated linker connects materialization to root and workspace dependency links. Public and hidden hoist passes retain their distinct version-selection rules; mutable directory dependencies refresh their contents. Modules paths are checked against both lexical and physical project boundaries before cleanup, including Windows junctions. Shared entries use graph-hashed paths, while selected real project-local copies can consume a project-local hidden hoist tree. Unversioned aliases are removed from the shared store. Automatic phantom selection and CLI install orchestration remain pending.

The Go hoisted linker places real directories, sharing compatible versions across workspace members and nesting conflicts. Root declarations outrank member and transitive preferences. Hoisting boundaries retain visible matching ancestors; members outside the root retain independent trees. Materialization orders parents before nested children and preserves build output only for complete placements vouched for by the caller.

The recorded Rust PM baseline rejects PnP installation before writes. Go install-policy helpers retain the Yarn-identity guard, Berry's default, environment and ancestor configuration precedence, and explicit engine-linker refusal. This does not affect read-only commands or add a PnP writer.

Node runtime augmentation and provisioning remain outside the Go executable. Its package scripts use Node from `PATH`.

The Go PM components also compute the reference manifest install-shape and local-directory freshness digests. These distinguish dependency and build-policy edits from unrelated manifest changes, and compare source content separately from filesystem timestamps. Installation-state orchestration remains incomplete.

Go install-state helpers preserve the existing project sidecars and record interrupted links independently of successful-state writes. Layout checks cover workspace slots, root-direct package identity and shared-store edge targets. Hoisted snapshots retain the ancestor placement visible to a member, while bare local links may remain dangling. The complete install command remains unimplemented.

Go freshness checks compare recorded lockfiles, manifests, workspace membership and copied local sources in the reference order. Stable build denials remain fresh; unknown or deferred build completion triggers work. A metadata-only touch refreshes the small sidecar after content agrees. Hoisted reuse also requires matching package content fingerprints and a completed previous link phase. Resolved settings hashing and CLI integration remain pending.

Go install-delta helpers hash package content separately from resolution metadata and preserve patch/alias and generator-content inputs. Strongly connected components produce stable subtree fingerprints and dependency-build phases, including non-building bridges between selected builds. Their install and lifecycle consumers remain pending.

Go install policy folds shared-store and hidden-hoist requests into one materialization choice. Explicit contradictory requests fail; Nub’s default hoist still permits sharing outside CI. Existing mixed store trees are classified as shared when any package entry is a symlink or Windows junction.
