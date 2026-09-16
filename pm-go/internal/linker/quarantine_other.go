//go:build !darwin

package linker

import "github.com/nubjs/nub/pm-go/internal/store"

// Quarantine attributes are macOS-specific. Other systems do no traversal.
func (*Quarantine) StripIndexed(string, store.PackageIndex) {}
func (*Quarantine) StripTree(string)                        {}
