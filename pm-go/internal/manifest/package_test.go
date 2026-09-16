package manifest

import (
	"reflect"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
)

func TestInstallManifestTolerance(t *testing.T) {
	p, err := ParsePackage([]byte("\ufeff" + `{"name":"pkg","version":null,"dependencies":{"a":"^1","bad":42},"devDependencies":null,"peerDependencies":["bad"],"optionalDependencies":true,"scripts":{"good":"node tool.js","tool":{"config":true}},"engines":["node >=8"],"unknown":{"preserved":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	if p.Name == nil || *p.Name != "pkg" || p.Version != nil || !reflect.DeepEqual(p.Dependencies, map[string]string{"a": "^1"}) || len(p.DevDependencies)+len(p.PeerDependencies)+len(p.OptionalDependencies)+len(p.Engines) != 0 || p.Scripts["good"] != "node tool.js" || len(p.Scripts) != 1 || p.Raw.Get("unknown") == nil {
		t.Fatal(p)
	}
	for _, input := range []string{`{"name":false}`, `{"version":1}`, `{"engines":true}`, `{"engines":42}`, `{"bundledDependencies":[1]}`, `{"bundleDependencies":"a"}`, `{"bundledDependencies":[],"bundleDependencies":1}`, `{"updateConfig":true}`, `{"updateConfig":{"ignoreDependencies":null}}`} {
		if _, err := ParsePackage([]byte(input)); err == nil {
			t.Fatal("invalid manifest accepted", input)
		}
	}
}

func TestBundlePrecedenceAndPeers(t *testing.T) {
	for _, input := range []string{`{"dependencies":{"b":"1","a":"1"},"bundledDependencies":true,"bundleDependencies":["wrong"]}`, `{"bundleDependencies":["wrong"],"bundledDependencies":true,"dependencies":{"a":"1","b":"1"}}`} {
		p, err := ParsePackage([]byte(input))
		if err != nil || !reflect.DeepEqual(p.BundledNames(), []string{"a", "b"}) {
			t.Fatal(p, err)
		}
	}
	p, err := ParsePackage([]byte(`{"bundledDependencies":null,"bundleDependencies":["fallback"],"peerDependencies":{"required":"^1","optional":"*","malformed":"*"},"peerDependenciesMeta":{"optional":{"optional":true},"malformed":{"optional":"true"}},"dependenciesMeta":{"a":{"injected":true},"b":{"built":false},"c":{"built":true}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.BundledNames(), []string{"fallback"}) || len(p.RequiredPeers()) != 2 || !p.OptionalPeer("optional") || p.OptionalPeer("malformed") {
		t.Fatal(p)
	}
	if !reflect.DeepEqual(p.DependencyMeta("injected", true), []string{"a"}) || !reflect.DeepEqual(p.DependencyMeta("built", false), []string{"b"}) {
		t.Fatal("dependency metadata")
	}
}

func TestWorkspaceShapesAndAuthoredEmptyFields(t *testing.T) {
	for _, raw := range []string{`"packages/*"`, `["packages/*","!packages/skip"]`, `{"packages":[]}`, `{"packages":[],"nohoist":[],"catalog":{},"catalogs":{}}`, `{"packages":["packages/*"],"catalog":{"a":"^1"},"catalogs":{"next":{"a":"^2"}}}`} {
		value, err := jsonvalue.Parse([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		workspaces, err := ParseWorkspaces(value)
		if err != nil {
			t.Fatal(raw, err)
		}
		encoded, err := workspaces.Value().MarshalJSON()
		if err != nil || string(encoded) != raw {
			t.Fatal(raw, string(encoded), err)
		}
	}
	for _, raw := range []string{`true`, `{}`, `{"pacakges":[]}`, `{"packages":null}`, `{"packages":[1]}`, `{"packages":[],"nohoist":false}`, `{"packages":[],"catalog":{"a":1}}`, `{"packages":[],"catalogs":{"a":null}}`} {
		value, _ := jsonvalue.Parse([]byte(raw))
		if _, err := ParseWorkspaces(value); err == nil {
			t.Fatal("invalid workspaces accepted", raw)
		}
	}
}
