package linker

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type BinOptions struct {
	ExtendNodePath   bool
	PreferSymlinked  *bool
	HiddenModulesDir string
	Warn             func(code, message string)
}

// CreateBinShim writes relocatable wrappers, or a POSIX symlink by default.
// target is an absolute path; callers validate package-relative declarations
// with ValidateBinTarget before resolving that path.
func CreateBinShim(binDir, name, target string, options BinOptions) error {
	if err := ValidateBinName(name); err != nil {
		return err
	}
	if !filepath.IsAbs(binDir) || !filepath.IsAbs(target) {
		return fmt.Errorf("bin linking requires absolute directory and target paths")
	}
	link := filepath.Join(binDir, filepath.FromSlash(name))
	parent := filepath.Dir(link)
	if runtime.GOOS == "windows" {
		for _, path := range binPaths(binDir, name) {
			_ = os.Remove(path)
		}
	}
	if err := os.MkdirAll(parent, 0755); err != nil && !(runtime.GOOS == "windows" && os.IsExist(err)) {
		return err
	}
	if runtime.GOOS != "windows" {
		removeBinFile(link)
	}
	if runtime.GOOS != "windows" && (options.PreferSymlinked == nil || *options.PreferSymlinked) {
		if err := os.Symlink(symlinkBinTarget(parent, target), link); err != nil {
			return err
		}
		_ = os.Chmod(target, 0755)
		return nil
	}
	launch := detectBinLaunch(target, options.Warn)
	if !launch.direct && name == launch.program {
		if options.Warn != nil {
			options.Warn("WARN_AUBE_BIN_SHIM_NAME_IS_INTERPRETER", fmt.Sprintf("skipping bin %q: it is named after the interpreter it needs (%s), so any wrapper would resolve back to itself", name, launch.program))
		}
		return nil
	}
	rel := relativeBinTarget(parent, target)
	if runtime.GOOS == "windows" {
		for _, shim := range []struct{ suffix, style, sep string }{{".cmd", "cmd", "\\"}, {".ps1", "powershell", "/"}, {"", "gitbash", "/"}} {
			nodePath := ""
			if options.ExtendNodePath {
				nodePath = shimNodePath(parent, binDir, options.HiddenModulesDir, shim.sep, ";")
			}
			path := link + shim.suffix
			body := renderBinShim(shim.style, launch, strings.ReplaceAll(rel, "/", shim.sep), nodePath)
			if err := writeBinFile(path, body); err != nil {
				return err
			}
		}
		return nil
	}
	nodePath := ""
	if options.ExtendNodePath {
		nodePath = shimNodePath(parent, binDir, options.HiddenModulesDir, "/", ":")
	}
	if err := os.WriteFile(link, []byte(renderBinShim("posix", launch, rel, nodePath)), 0755); err != nil {
		return err
	}
	if err := os.Chmod(link, 0755); err != nil {
		return err
	}
	if launch.direct {
		_ = os.Chmod(target, 0755)
	}
	return nil
}

func binPaths(binDir, name string) []string {
	path := filepath.Join(binDir, filepath.FromSlash(name))
	paths := []string{path}
	if runtime.GOOS == "windows" {
		paths = append(paths, path+".cmd", path+".ps1")
	}
	return paths
}

func writeBinFile(path, body string) error {
	err := os.WriteFile(path, []byte(body), 0644)
	if os.IsExist(err) {
		_ = os.Remove(path)
		err = os.WriteFile(path, []byte(body), 0644)
	}
	return err
}

func RemoveBinShim(binDir, name string) {
	if ValidateBinName(name) != nil {
		return
	}
	for _, path := range binPaths(binDir, name) {
		removeBinFile(path)
	}
	if parent := filepath.Dir(filepath.Join(binDir, filepath.FromSlash(name))); parent != binDir {
		_ = os.Remove(parent)
	}
}

func relativeBinTarget(parent, target string) string {
	if runtime.GOOS == "windows" {
		parent, target = stripVerbatim(parent), stripVerbatim(target)
	}
	rel, err := filepath.Rel(parent, target)
	if err != nil {
		rel = target
	}
	return strings.ReplaceAll(strings.ToValidUTF8(rel, "\ufffd"), "\\", "/")
}

func stripVerbatim(path string) string {
	if strings.HasPrefix(path, `\\?\UNC\`) {
		return `\\` + strings.TrimPrefix(path, `\\?\UNC\`)
	}
	return strings.TrimPrefix(path, `\\?\`)
}

func symlinkBinTarget(parent, target string) string {
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		return target
	}
	surface := relativeBinTarget(parent, target)
	if path, err := filepath.EvalSymlinks(filepath.Join(parent, surface)); err == nil && path == resolved {
		return surface
	}
	if physical, err := filepath.EvalSymlinks(parent); err == nil {
		return relativeBinTarget(physical, resolved)
	}
	return target
}

func shimNodePath(parent, binDir, hidden, pathSep, listSep string) string {
	prefix := "$basedir/"
	if pathSep == "\\" {
		prefix = "%~dp0"
	}
	entry := func(target string) string {
		return prefix + strings.ReplaceAll(relativeBinTarget(parent, target), "/", pathSep)
	}
	paths := []string{entry(filepath.Dir(binDir))}
	if hidden != "" {
		paths = append(paths, entry(hidden))
	}
	return strings.Join(paths, listSep)
}
