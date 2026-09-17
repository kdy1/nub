package installconfig

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/settings"
)

func TestStoragePathsAreInvocationScoped(t *testing.T) {
	root := t.TempDir()
	in := StorageInput{Project: filepath.Join(root, "project"), Home: filepath.Join(root, "home"), Temp: filepath.Join(root, "tmp"), OS: "linux"}
	s, err := ResolveStorage(in)
	if err != nil {
		t.Fatal(err)
	}
	if s.Files != filepath.Join(in.Home, ".local/share/nub-pm-go/store/v1/files") || s.GlobalStore != filepath.Join(in.Home, ".cache/nub-pm-go/store/v1") {
		t.Fatal(s)
	}
	items, err := os.ReadDir(s.Files)
	if err != nil || len(items) != 0 {
		t.Fatal(items, err)
	}
	in.Settings.CLI = []settings.Entry{{"cache-dir", "./from-cli"}, {"store-dir", "~/custom-store"}, {"global-virtual-store-dir", "./global-tree"}}
	in.Env = map[string]string{"NUB_CACHE_DIR": "./from-env", "AUBE_CACHE_DIR": "./ignored"}
	s, err = ResolveStorage(in)
	if err != nil {
		t.Fatal(err)
	}
	if s.Cache != filepath.Join(in.Project, "from-env") || s.Files != filepath.Join(in.Home, "custom-store/v1/files") || s.GlobalStore != filepath.Join(in.Project, "global-tree/v1") {
		t.Fatal(s)
	}
	native := "native-cache"
	in.CacheOverride = &native
	s, err = ResolveStorage(in)
	if err != nil || s.Cache != filepath.Join(in.Project, native) {
		t.Fatal(s, err)
	}
	in.Project = filepath.Join(root, "second")
	in.CacheOverride = nil
	s, err = ResolveStorage(in)
	if err != nil || s.Cache != filepath.Join(in.Project, "from-env") {
		t.Fatal("another invocation inherited paths", s, err)
	}
}

func TestStorageFallbackOnlyForUnwritableDefaults(t *testing.T) {
	root := t.TempDir()
	configured := filepath.Join(root, "configured")
	for _, err := range []error{os.ErrPermission, syscall.EROFS, os.ErrNotExist, syscall.ENOSPC} {
		warnings := 0
		c := settings.Context{Defaults: []settings.Entry{{"storeDir", configured}}, Warn: func(code, message string) {
			warnings++
			if code != "WARN_AUBE_STORE_FALLBACK" || !strings.Contains(message, "project-local store") {
				t.Fatal(code, message)
			}
		}}
		in := StorageInput{Project: filepath.Join(root, "project"), Temp: root, Home: root, Settings: c, Probe: func(string) error { return err }}
		s, e := ResolveStorage(in)
		if e != nil {
			t.Fatal(e)
		}
		fallback := errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.EROFS)
		if (s.ReadFallback != "") != fallback || warnings != map[bool]int{true: 1, false: 0}[fallback] {
			t.Fatal(err, s, warnings)
		}
		if fallback && s.Files != filepath.Join(in.Project, "node_modules/.nub-store/v1/files") {
			t.Fatal(s)
		}
		// An explicit spelling of the profile default still gets fallback.
		in.Settings.CLI = []settings.Entry{{"storeDir", configured}}
		if explicit, e := ResolveStorage(in); e != nil || explicit != s {
			t.Fatal(explicit, e, s)
		}
		in.Settings.CLI = []settings.Entry{{"storeDir", filepath.Join(root, "different")}}
		in.Probe = func(string) error { t.Fatal("probed a custom store"); return nil }
		s, e = ResolveStorage(in)
		if e != nil || s.ReadFallback != "" {
			t.Fatal(s, e)
		}
	}
}

func TestHomelessStorageUsesIndependentAvailableDefaults(t *testing.T) {
	root := t.TempDir()
	in := StorageInput{Project: root, Temp: root, OS: "linux", Env: map[string]string{"XDG_CACHE_HOME": filepath.Join(root, "cache")}}
	s, err := ResolveStorage(in)
	if err != nil {
		t.Fatal(err)
	}
	if s.Files != filepath.Join(root, "nub-pm-go/store/v1/files") || s.Cache != filepath.Join(root, "cache/nub-pm-go") {
		t.Fatal(s)
	}
	in.Env = map[string]string{"XDG_DATA_HOME": filepath.Join(root, "data")}
	in.Settings.CLI = []settings.Entry{{"cacheDir", "~/unavailable"}, {"storeDir", "~/unavailable"}}
	s, err = ResolveStorage(in)
	if err != nil {
		t.Fatal(err)
	}
	if s.Files != filepath.Join(root, "data/nub-pm-go/store/v1/files") || s.Cache != filepath.Join(root, "nub-pm-go") {
		t.Fatal(s)
	}
	opened := s.NewStore()
	defer opened.Close()
	if opened.Root != s.Files || opened.CacheDir != s.Cache {
		t.Fatal(opened)
	}
}
