package store

import (
	"path/filepath"
	"testing"
)

func TestDefaultDirs(t *testing.T) {
	for _, platform := range []string{"linux", "darwin", "windows"} {
		root, cache, err := DefaultDirs(platform, "home", nil)
		if err != nil || root != filepath.Join("home", ".local/share/nub-pm-go/store/v1/files") || cache != filepath.Join("home", ".cache/nub-pm-go") {
			t.Fatalf("%s: %s %s %v", platform, root, cache, err)
		}
		env := map[string]string{"LOCALAPPDATA": "local", "XDG_DATA_HOME": "data", "XDG_CACHE_HOME": "cache"}
		root, cache, err = DefaultDirs(platform, "home", env)
		wantData := "data"
		if platform == "windows" {
			wantData = "local"
		}
		if err != nil || root != filepath.Join(wantData, "nub-pm-go/store/v1/files") || cache != filepath.Join("cache", "nub-pm-go") {
			t.Fatalf("%s: %s %s %v", platform, root, cache, err)
		}
	}
	if _, _, err := DefaultDirs("linux", "", nil); err == nil {
		t.Fatal("missing home accepted")
	}
	root, cache, err := DefaultDirs("windows", "", map[string]string{"LOCALAPPDATA": "local"})
	if err != nil || root == "" || cache == "" {
		t.Fatal(root, cache, err)
	}
}
