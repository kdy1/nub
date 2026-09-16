package installstate

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/fsutil"
	"github.com/nubjs/nub/pm-go/internal/identity"
	"github.com/nubjs/nub/pm-go/internal/linker"
	"lukechampine.com/blake3"
)

// Paths contains resolved per-invocation paths. State keeps Nub's existing
// project format; only the global Go store/cache use a separate namespace.
type Paths struct{ Project, Modules, State, Branch string }

// NewPaths accepts an expanded state parent. Empty uses Nub's adapter default;
// the engine's literal node_modules default follows a modulesDir override.
func NewPaths(project, modulesName, stateParent, branch string) (Paths, error) {
	if modulesName == "" {
		modulesName = "node_modules"
	}
	modules, err := linker.CheckedModulesDir(project, modulesName)
	if err != nil {
		return Paths{}, err
	}
	switch stateParent {
	case "":
		stateParent = filepath.Join(project, "node_modules", ".store")
	case "node_modules":
		stateParent = modules
	default:
		stateParent = join(project, stateParent)
	}
	return Paths{filepath.Clean(project), modules, filepath.Join(stateParent, ".nub-state"), branch}, nil
}

func (p Paths) Read() *State {
	if p.removeLegacy() != nil {
		return nil
	}
	var s State
	if !readJSON(filepath.Join(p.State, "state.json"), &s) {
		return nil
	}
	return &s
}

// ReadFreshness migrates older successful state once. Failure to cache the
// projection does not discard the successfully parsed source state.
func (p Paths) ReadFreshness() *Freshness {
	if p.removeLegacy() != nil {
		return nil
	}
	var f Freshness
	if readJSON(filepath.Join(p.State, "fresh.json"), &f) {
		return &f
	}
	if s := p.Read(); s != nil {
		f = s.Freshness()
		_ = p.WriteFreshness(&f)
		return &f
	}
	return nil
}

func (p Paths) Write(s *State) error {
	if s == nil {
		return fmt.Errorf("nil install state")
	}
	if err := p.removeLegacy(); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(p.State, "state.json"), s); err != nil {
		return err
	}
	f := s.Freshness()
	return p.WriteFreshness(&f)
}

func (p Paths) WriteFreshness(f *Freshness) error {
	return writeJSON(filepath.Join(p.State, "fresh.json"), f)
}

func (p Paths) removeLegacy() error {
	if info, err := os.Stat(p.State); err == nil && info.Mode().IsRegular() {
		return os.Remove(p.State)
	}
	return nil
}

func (p Paths) MarkLinkInProgress() error {
	if err := os.MkdirAll(p.State, 0755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(p.State, "link-in-progress"), nil, 0666)
}
func (p Paths) ClearLinkInProgress() error {
	return removeFile(filepath.Join(p.State, "link-in-progress"))
}
func (p Paths) LinkCompletedCleanly() bool {
	_, err := os.Stat(filepath.Join(p.State, "link-in-progress"))
	return err != nil
}

func (p Paths) Remove() error { return os.RemoveAll(p.State) }

func (p Paths) WritePlacements(placements linker.HoistedPlacements) error {
	file := filepath.Join(p.State, "hoisted-placements.json")
	if placements == nil {
		if info, err := os.Stat(p.State); err == nil && info.Mode().IsRegular() {
			return nil
		}
		return removeFile(file)
	}
	if err := p.removeLegacy(); err != nil {
		return err
	}
	recorded := map[string][]string{}
	for key, paths := range placements {
		for _, path := range paths {
			recorded[key] = append(recorded[key], relative(path, p.Project))
		}
	}
	return writeJSON(file, recorded)
}

func (p Paths) ReadPlacements() linker.HoistedPlacements {
	var recorded map[string][]string
	if !readJSON(filepath.Join(p.State, "hoisted-placements.json"), &recorded) {
		return nil
	}
	placements := linker.HoistedPlacements{}
	for key, paths := range recorded {
		placements[key] = []string{}
		for _, path := range paths {
			path = join(p.Project, path)
			if _, err := os.Stat(path); err == nil {
				placements[key] = append(placements[key], path)
			}
		}
	}
	return placements
}

func (p Paths) ReadLicenses() Licenses {
	var s Licenses
	if readJSON(filepath.Join(p.State, "licenses.json"), &s) || readJSON(filepath.Join(p.Project, "node_modules", ".nub-state", "licenses.json"), &s) {
		return s
	}
	return Licenses{Licenses: map[string]string{}}
}
func (p Paths) WriteLicenses(s *Licenses) error {
	return writeJSON(filepath.Join(p.State, "licenses.json"), s)
}

func (p Paths) ActiveLockfile() (name, path string) { return ActiveLockfile(p.Project, p.Branch) }
func ActiveLockfile(project, branch string) (name, path string) {
	preferred := identity.Nub.BranchFilename(branch)
	candidates := []string{preferred, "nub.lock", "lock.yaml", identity.Pnpm.BranchFilename(branch), "pnpm-lock.yaml", "bun.lock", "yarn.lock", "npm-shrinkwrap.json", "package-lock.json"}
	for _, name := range candidates {
		path := filepath.Join(project, name)
		if _, err := os.Stat(path); err == nil {
			return name, path
		}
	}
	return preferred, ""
}

func CaptureMeta(path string) *FileMeta {
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	m := &FileMeta{Size: uint64(info.Size())}
	if t := info.ModTime(); t.Unix() >= 0 {
		m.MtimeSecs, m.MtimeNanos = t.Unix(), uint32(t.Nanosecond())
	}
	return m
}

func HashBytes(data []byte) string {
	h := blake3.Sum256(data)
	return "blake3:" + hex.EncodeToString(h[:])
}
func HashFile(path string) string { data, _ := os.ReadFile(path); return HashBytes(data) }
func hashFileIfExists(path string) *string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	h := HashBytes(data)
	return &h
}
func relative(path, base string) string {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		rel = path
	}
	return strings.ReplaceAll(strings.ToValidUTF8(rel, "\ufffd"), "\\", "/")
}
func join(base, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(base, filepath.FromSlash(path))
}
func removeFile(path string) error {
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
func readJSON(path string, out any) bool {
	data, err := os.ReadFile(path)
	return err == nil && decode(data, out) == nil
}
func writeJSON(path string, value any) error {
	data, err := encode(value)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return fsutil.WriteDefault(path, data)
}
