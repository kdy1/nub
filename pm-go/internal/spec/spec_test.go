package spec

import "testing"

func TestSplitCoordinates(t *testing.T) {
	for _, tc := range []struct {
		input, name, version string
		explicit             bool
	}{
		{"pkg", "pkg", "", false}, {"pkg@^1", "pkg", "^1", true}, {"pkg@", "pkg", "", true}, {"@scope/pkg", "@scope/pkg", "", false}, {"@scope/pkg@next", "@scope/pkg", "next", true}, {"@scope/pkg@npm:@other/pkg@^1", "@scope/pkg", "npm:@other/pkg@^1", true},
	} {
		name, version, explicit := Split(tc.input)
		if name != tc.name || version != tc.version || explicit != tc.explicit {
			t.Fatal(tc, name, version, explicit)
		}
	}
}

func TestWorkspaceSpecGrammar(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  Workspace
	}{
		{"workspace:pkg@*", Workspace{Kind: "alias", Name: "pkg", Range: "*"}},
		{"workspace:@scope/pkg@^1", Workspace{Kind: "alias", Name: "@scope/pkg", Range: "^1"}},
		{"workspace:pkg", Workspace{Kind: "range", Range: "pkg"}},
		{"workspace:packages/pkg", Workspace{Kind: "range", Range: "packages/pkg"}},
		{"workspace:../pkg", Workspace{Kind: "path", Path: "../pkg"}},
		{"workspace:./pkg", Workspace{Kind: "path", Path: "./pkg"}},
		{"workspace:_private@1", Workspace{Kind: "range", Range: "_private@1"}},
		{"workspace:/absolute@1", Workspace{Kind: "range", Range: "/absolute@1"}},
		{"workspace:", Workspace{Kind: "range", Range: ""}},
	} {
		got, ok := ParseWorkspace(tc.input)
		if !ok || got != tc.want {
			t.Fatal(tc, got, ok)
		}
	}
	if _, ok := ParseWorkspace("^1"); ok {
		t.Fatal("plain spec accepted as workspace")
	}
	for _, sigil := range []string{"", "*", "^", "~", "packages/pkg"} {
		if !WorkspaceRangeBinds("0.0.0-dev", sigil) {
			t.Fatal(sigil)
		}
	}
	if !WorkspaceRangeBinds("1.2.3", "^1") || WorkspaceRangeBinds("1.2.3", "^2") {
		t.Fatal("workspace range mismatch")
	}
}
