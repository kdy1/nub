package installconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/linker"
	"github.com/nubjs/nub/pm-go/internal/manifest"
)

func boolp(b bool) *bool { return &b }
func TestNubMaterializationSelection(t *testing.T) {
	cases := []struct {
		input         MaterializationInput
		want          Materialization
		errorContains string
	}{
		{MaterializationInput{ResolvedHoist: true}, SharedStore, ""},
		{MaterializationInput{ResolvedHoist: true, Env: map[string]string{"CI": ""}}, DiskWithHiddenTree, ""},
		{MaterializationInput{Env: map[string]string{"CI": "false"}}, DiskWithoutHiddenTree, ""},
		{MaterializationInput{ResolvedHoist: true, HoistExplicit: boolp(true)}, DiskWithHiddenTree, ""},
		{MaterializationInput{ResolvedHoist: true, EnableGlobalVirtualStore: boolp(false)}, DiskWithHiddenTree, ""},
		{MaterializationInput{ResolvedHoist: false, EnableGlobalVirtualStore: boolp(false)}, DiskWithoutHiddenTree, ""},
		{MaterializationInput{ResolvedHoist: true, EnableGlobalVirtualStore: boolp(true), Env: map[string]string{"CI": "1"}}, SharedStore, ""},
		{MaterializationInput{ResolvedHoist: true, Linker: Hoisted}, DiskWithHiddenTree, ""},
		{MaterializationInput{Linker: Hoisted}, DiskWithoutHiddenTree, ""},
		{MaterializationInput{EnableGlobalVirtualStore: boolp(true), HoistExplicit: boolp(true)}, 0, "conflicts with hoist=true"},
		{MaterializationInput{EnableGlobalVirtualStore: boolp(true), HoistExplicit: boolp(true), Linker: Hoisted}, 0, "conflicts with node-linker=hoisted"},
	}
	for _, c := range cases {
		got, err := SelectMaterialization(c.input)
		if c.errorContains != "" {
			if err == nil || !strings.Contains(err.Error(), c.errorContains) {
				t.Fatal(c, got, err)
			}
			continue
		}
		if err != nil || got.Mode != c.want || got.Mode.UsesSharedStore() && got.Mode.BuildsHiddenTree() {
			t.Fatal(c, got, err)
		}
		if prewarm := got.PrewarmOverride(Isolated); prewarm == nil || *prewarm != got.Mode.UsesSharedStore() {
			t.Fatal(got, prewarm)
		}
		if prewarm := got.PrewarmOverride(Hoisted); prewarm != got.Override {
			t.Fatal(got, prewarm)
		}
	}
}

func TestCompatibilityTriggersAndOverrides(t *testing.T) {
	m, err := manifest.ParsePackage([]byte(`{"dependencies":{"z-next":"1"},"devDependencies":{"a-next":"1"},"optionalDependencies":{"optional":"1"},"peerDependencies":{"peer-only":"1"}}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		patterns []string
		want     string
	}{
		{[]string{"*-next"}, "z-next"}, {[]string{"peer-only"}, ""}, {[]string{"optional", "*-next"}, "optional"}, {[]string{"NEXT"}, ""}, {[]string{"[a-z]-next"}, "z-next"},
	} {
		if got := FindGVSIncompatibleTrigger([]*manifest.Package{m}, c.patterns); got != c.want {
			t.Fatal(c, got)
		}
	}
	input := MaterializationInput{ResolvedHoist: true, Manifests: []*manifest.Package{m}, DisableForPackages: []string{"*-next"}}
	selection, err := SelectMaterialization(input)
	if err != nil || selection.Mode != DiskWithHiddenTree || selection.IncompatiblePackage != "z-next" {
		t.Fatal(selection, err)
	}
	input.VirtualStoreOnly = true
	selection, err = SelectMaterialization(input)
	if err != nil || selection.Mode != SharedStore || selection.IncompatiblePackage != "" {
		t.Fatal(selection, err)
	}
	input.VirtualStoreOnly = false
	input.EnableGlobalVirtualStore = boolp(true)
	selection, err = SelectMaterialization(input)
	if err != nil || selection.Mode != SharedStore || selection.IncompatiblePackage != "" {
		t.Fatal(selection, err)
	}
}

func TestStoreModeMixedAndDanglingEntries(t *testing.T) {
	root := t.TempDir()
	if DetectStoreMode(filepath.Join(root, "missing")) != nil || DetectStoreMode(root) != nil {
		t.Fatal("empty classified")
	}
	for _, name := range []string{"node_modules", ".nub-state"} {
		if err := os.Mkdir(filepath.Join(root, name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "plain-file"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	if DetectStoreMode(root) != nil {
		t.Fatal("non-packages classified")
	}
	if err := os.Mkdir(filepath.Join(root, "@scope+name@1"), 0755); err != nil {
		t.Fatal(err)
	}
	if mode := DetectStoreMode(root); mode == nil || *mode {
		t.Fatal(mode)
	}
	if err := linker.CreateDirLink(t.Context(), filepath.Join(root, "missing"), filepath.Join(root, "z-link@1")); err != nil {
		t.Fatal(err)
	}
	if mode := DetectStoreMode(root); mode == nil || !*mode {
		t.Fatal("mixed tree misclassified", mode)
	}
}
