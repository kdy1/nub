# Go package manager

`nub-pm-go` is an independent Go implementation of Nub's package manager. It is under development; registered commands without a handler return a nonzero exit status. The existing Rust executable is unchanged.

Build from this directory with `go build -o ../target/pm-go/ ./cmd/nub-pm-go`. Run checks with `go test -race ./...` and `go vet ./...`; the Go package manager workflow runs them on Linux, macOS, and Windows.

The command inventory in `internal/surface/commands.json` is checked against Nub's Rust registry. Its reference revision is `2a4573ef059798b75f789aa8c22da51e470e3138`.

The Go executable uses its own global cache and store. Lifecycle scripts and fetched tools use Node from `PATH`; Nub's runtime augmentation, TypeScript transformation, and Node provisioning are outside this executable.
