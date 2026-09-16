# Go package manager

`nub-pm-go` is an independent Go implementation of Nub's package manager. It is under development; registered commands without a handler return a nonzero exit status. The existing Rust executable is unchanged.

Build from this directory with `go build -o ../target/pm-go/ ./cmd/nub-pm-go`. Run checks with `go test -race ./...` and `go vet ./...`; the Go package manager workflow runs them on Linux, macOS, and Windows.

The command inventory in `internal/surface/commands.json` is checked against Nub's Rust registry. Its reference revision is `2a4573ef059798b75f789aa8c22da51e470e3138`.

Implemented handlers: `pkg get/set/delete/fix` and `set-script` (`ss`). Manifest edits preserve key order, indentation, line endings, and trailing-newline style. Invalid multi-key edits leave the file unchanged. The Go workflow also builds the Rust reference and compares both executables on the same manifest fixtures.

The project model includes PM declaration and lockfile selection, major-specific override policy, and the npmrc registry configuration layer. Registry credentials retain their source scope; project files cannot expand environment secrets, configure token helpers or proxies, or disable TLS validation. These internal components are not yet connected to an installation handler.

The semver layer supports npm comparator sets, OR, partial and wildcard versions, hyphen ranges, tilde/caret ranges, and prerelease admission by version tuple. CI compares its results with the repository's pinned `node-semver` 7.7.4 fixture; Node is a test oracle and is not required for Go version resolution.

Registry metadata parsing preserves raw fields and normalizes legacy dependency, platform, bin, license, and funding shapes. Invalid integrity metadata is rejected. Version selection implements lockfile and dist-tag preferences, deprecation ordering, and publish-age gates, including the bounded fallback for an age-blocked `latest` tag.

Registry transport uses source-scoped credentials, TLS roots and client certificates, and explicit proxy settings. It blocks HTTPS downgrades, strips credentials across redirect authorities, limits response bodies, and bounds timeout retries. Tests use isolated HTTP and TLS servers.

The Go executable uses its own global cache and store. Lifecycle scripts and fetched tools use Node from `PATH`; Nub's runtime augmentation, TypeScript transformation, and Node provisioning are outside this executable.
