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
| Context and parser | Explicit directory, environment, streams; manifest option/variadic parsing | Full pnpm grammar, option-position and exit/output parity |
| Project and config | PM identity/major override rules, source-scoped npmrc/auth/TLS/proxies, ordered manifests, workspace glob matching | Complete settings session, nub.jsonc consumers, Yarn/Bun adapters, workspace discovery and filters |
| Resolution | npm semver oracle, version/publish-age selection, source identities, platforms, graph traversal and optional/peer passes | Full dependency resolution, peer contexts/conflicts, overrides/catalog expansion, Git/generator execution |
| Lockfiles | npm v1/v2/v3/versionless reader and v3 writer; canonical hoist layout and patch selector resolution; native npm fixture round trips; pnpm peer/patch/alias keys, checksums, typed YAML document selection, v9+ reader/writer and output layout rules | Bun and Yarn adapters; frozen/drift orchestration; unchanged-file policy |
| Network and store | HTTP/TLS/auth/redirect limits, metadata caching/offline, SRI, safe archives, BLAKE3 CAS/indexes, process locks, directory/tarball imports | Complete package fetch coordinator, integrity-free source bindings, maintenance commands, installation state |
| Linkers | Graph and placement prerequisites | Isolated/hoisted/PnP materialization, executable shims, phantom dependencies, reuse |
| Scripts | No lifecycle execution handler | Build approvals, ordering, sandbox, environment, exit propagation, node-gyp bootstrap |
| Remaining commands | Verb and alias registry | Queries/audit, pack/publish/auth, configuration/cache/PM management, dlx/create |

## Validation boundary

- CI runs Go tests with the race detector, vet, and cgo-disabled builds on Linux, macOS, and Windows.
- Rust differential tests cover manifest commands, npm library serialization, and pnpm graph parsing. A test-only probe uses the unchanged Rust libraries; production Go never loads it. These cases do not establish install or full PM parity.
- The pinned npm oracle exercises lockfile round trips and clean installs against an isolated registry. It does not run the Go installer, which has no handler yet.
- A workspace conflict fixture retains the reference writer's redundant member-local hoist. Its difference from native npm is asserted explicitly, without normalizing it out of Rust/Go byte comparisons. The install orchestrator's unchanged-lockfile policy is still unimplemented.
- Node-semver 7.7.4 is an independent range oracle. Production range handling is Go code.
- Full Rust regressions, interruption/retry/concurrent-install scenarios, every incumbent lockfile acceptance matrix, and performance measurements remain unverified.

## Deliberate exclusions

Nub runtime augmentation, automatic TypeScript transformation, Node provisioning, and Nub self-version delegation are outside this executable. Scripts and fetched tools use PATH Node. Rust sources and the default Nub PM path are retained. Go global state uses the separate `nub-pm-go` namespace. Production code does not invoke the Rust engine.
