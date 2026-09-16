package resolver

import (
	"reflect"
	"testing"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/manifest"
)

func TestDirectSeedPriorityAndOptionalPeers(t *testing.T) {
	root, err := manifest.ParsePackage([]byte(`{"dependencies":{"prod":"1","duplicate":"2"},"devDependencies":{"dev":"3","duplicate":"4"},"optionalDependencies":{"opt":"5","dev":"6","ignored":"7"},"peerDependencies":{"peer":"8","prod":"9","ignored":"10","optionalPeer":"11"},"peerDependenciesMeta":{"optionalPeer":{"optional":true}}}`))
	if err != nil {
		t.Fatal(err)
	}
	member, err := manifest.ParsePackage([]byte(`{"dependencies":{"member":"catalog:"}}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, auto := range []bool{false, true} {
		queue, importers := seedDirectDependencies([]lockfile.ImporterManifest{{Path: ".", Package: root}, {Path: "packages/member", Package: member}}, lockfile.Set{"ignored": {}}, auto)
		want := []string{"duplicate:2:dependencies:.", "prod:1:dependencies:.", "dev:3:devDependencies:.", "opt:5:optionalDependencies:."}
		if auto {
			want = append(want, "peer:8:dependencies:.")
		}
		want = append(want, "member:catalog::dependencies:packages/member")
		var got []string
		for _, task := range queue {
			got = append(got, task.Name+":"+task.Range+":"+task.Type.Label()+":"+task.Importer)
			if !task.Root || task.Parent != nil || task.OriginalSpecifier == nil || *task.OriginalSpecifier != task.Range || task.registryName() != task.Name || *task.lockfileSpecifier() != task.Range {
				t.Fatal(task)
			}
		}
		if !reflect.DeepEqual(got, want) || len(importers) != 2 {
			t.Fatal(got, want, importers)
		}
	}
}
