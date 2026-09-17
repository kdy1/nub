package installconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/nubjs/nub/pm-go/internal/settings"
	"github.com/nubjs/nub/pm-go/internal/store"
)

type StorageInput struct {
	Settings                settings.Context
	Project, Home, Temp, OS string
	Env                     map[string]string
	// Native path overrides outrank textual settings, matching embedded calls.
	StoreOverride, CacheOverride *string
	SandboxLabel                 string
	// Probe is normally nil. Tests can model permission failures independently
	// of administrator/root privileges and platform ACL differences.
	Probe func(string) error
}

// Storage is resolved once for an invocation, then reused by all phases. A
// fallback's global CAS is read-only, and an explicitly chosen store never
// silently falls back to a different location.
type Storage struct {
	Files, Cache, GlobalStore, ReadFallback string
}

func (s Storage) NewStore() *store.Store {
	result := store.New(s.Files, s.Cache)
	result.ReadFallback = s.ReadFallback
	return result
}

func ResolveStorage(in StorageInput) (Storage, error) {
	var result Storage
	if !filepath.IsAbs(in.Project) || !filepath.IsAbs(in.Temp) || in.Home != "" && !filepath.IsAbs(in.Home) {
		return result, fmt.Errorf("storage settings require absolute invocation paths")
	}
	platform := in.OS
	if platform == "" {
		platform = runtime.GOOS
	}
	files, cache, _ := store.DefaultDirs(platform, in.Home, in.Env)
	if files == "" {
		files = filepath.Join(in.Temp, store.Namespace, "store/v1/files")
	}
	if cache == "" {
		cache = filepath.Join(in.Temp, store.Namespace)
	}
	// Environment-supplied XDG paths may be relative. The reference resolves
	// them relative to its process cwd; Go uses the invocation directory.
	files, cache = anchor(files, in.Project), anchor(cache, in.Project)
	result.Cache = cache
	c := in.Settings
	if in.CacheOverride != nil {
		result.Cache = anchor(*in.CacheOverride, in.Project)
	} else if raw := in.Env["NUB_CACHE_DIR"]; raw != "" {
		if path, ok := expandPath(raw, in.Project, in.Home); ok {
			result.Cache = path
		}
	} else if raw := c.String("cacheDir"); raw != nil {
		if path, ok := expandPath(*raw, in.Project, in.Home); ok {
			result.Cache = path
		}
	}
	result.GlobalStore = filepath.Join(result.Cache, "store")
	if raw := c.String("globalVirtualStoreDir"); raw != nil {
		if path, ok := expandPath(*raw, in.Project, in.Home); ok {
			result.GlobalStore = path
		}
	}
	result.GlobalStore = filepath.Join(result.GlobalStore, "v1")
	var custom *string
	if in.StoreOverride != nil {
		value := anchor(*in.StoreOverride, in.Project)
		custom = &value
	} else if raw := c.String("storeDir"); raw != nil {
		if path, ok := expandPath(*raw, in.Project, in.Home); ok {
			custom = &path
		}
	}
	if custom != nil {
		files = filepath.Join(*custom, "v1/files")
		isDefault := false
		for _, e := range c.Defaults {
			if e[0] == "storeDir" {
				if path, ok := expandPath(e[1], in.Project, in.Home); ok {
					isDefault = path == *custom
				}
				break
			}
		}
		if !isDefault {
			result.Files = files
			return result, nil
		}
	}
	result.Files = files
	probe := in.Probe
	if probe == nil {
		probe = probeWritable
	}
	if err := probe(files); errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.EROFS) {
		result.ReadFallback = files
		result.Files = filepath.Join(in.Project, "node_modules/.nub-store/v1/files")
		if c.Warn != nil {
			sandbox := ""
			if in.SandboxLabel != "" {
				sandbox = " (inside the " + in.SandboxLabel + " sandbox)"
			}
			c.Warn("WARN_AUBE_STORE_FALLBACK", fmt.Sprintf("store %s is not writable%s; new packages go to the project-local store %s for this run", files, sandbox, result.Files))
		}
	}
	return result, nil
}

func anchor(path, project string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(project, path)
}

func probeWritable(root string) error {
	if err := os.MkdirAll(root, 0755); err != nil {
		return err
	}
	file, err := os.CreateTemp(root, ".write-probe-*")
	if err != nil {
		return err
	}
	path := file.Name()
	_ = file.Close()
	_ = os.Remove(path)
	return nil
}
