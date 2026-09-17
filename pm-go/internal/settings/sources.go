package settings

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"

	"github.com/nubjs/nub/pm-go/internal/npmconfig"
	"go.yaml.in/yaml/v4"
)

type FileInput struct {
	Files             npmconfig.Files
	ProjectConfig     []Entry
	SystemManagedPath *string
	Warn              func(string)
}

type FileSources struct {
	Npmrc                  NpmrcSources
	Managed, ProjectConfig []Entry
	GlobalYAML             *yaml.Node
	Pnpm                   bool
}

// LoadSources reads the invocation's file tiers once. The host supplies its
// validated/lowered native project settings; Nub has no aube TOML file tier.
func LoadSources(in FileInput) (FileSources, error) {
	files := in.Files
	if !filepath.IsAbs(files.Dir) || files.Home != "" && !filepath.IsAbs(files.Home) {
		return FileSources{}, fmt.Errorf("settings sources require absolute invocation paths")
	}
	managed, err := LoadManagedFiles(ManagedFiles{Dir: files.Dir, Env: files.Env, SystemPath: in.SystemManagedPath, Warn: in.Warn})
	if err != nil {
		return FileSources{}, err
	}
	result := FileSources{Npmrc: LoadNpmrcSources(files), Managed: managed, ProjectConfig: slices.Clone(in.ProjectConfig), Pnpm: files.Pnpm}
	if files.Pnpm && files.Pnpm11 {
		platform := files.OS
		if platform == "" {
			platform = runtime.GOOS
		}
		if dir := npmconfig.PnpmConfigDir(platform, files.Home, files.Env); dir != "" {
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(files.Dir, dir)
			}
			if data, err := os.ReadFile(filepath.Join(dir, "config.yaml")); err == nil {
				// Global config is best effort, including malformed or empty
				// documents. A bad value invalidates the whole source tier.
				result.GlobalYAML, _ = ParseYAMLSource(data)
			}
		}
	}
	return result, nil
}

func (s FileSources) Context(workspace *yaml.Node, env, cli []Entry) Context {
	return Context{Managed: slices.Clone(s.Managed), ProjectConfig: slices.Clone(s.ProjectConfig), UserNpmrc: slices.Clone(s.Npmrc.User), ProjectNpmrc: slices.Clone(s.Npmrc.Project), GlobalYAML: s.GlobalYAML, WorkspaceYAML: workspace, Pnpm: s.Pnpm, Env: slices.Clone(env), CLI: slices.Clone(cli)}
}

// LoadWorkspaceYAML is strict for an incumbent pnpm project. The identity gate
// is checked before filesystem access, even for malformed foreign files.
func LoadWorkspaceYAML(dir string, pnpm bool) (*yaml.Node, error) {
	if !pnpm {
		return nil, nil
	}
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("workspace settings require an absolute invocation directory")
	}
	path := filepath.Join(dir, "pnpm-workspace.yaml")
	if _, err := os.Stat(path); err != nil {
		return nil, nil // Path::exists is false for missing/inaccessible paths.
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	node, err := ParseYAMLSource(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return node, nil
}
