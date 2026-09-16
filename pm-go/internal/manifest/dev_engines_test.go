package manifest

import (
	"reflect"
	"testing"
)

func TestTolerantEngineEntries(t *testing.T) {
	p, err := ParsePackage([]byte(`{"devEngines":{"runtime":[false,{"name":"node","version":5},{"name":"node","onFail":"invalid"},{"name":"node","version":"^22","onFail":"download"},{"name":"node","version":"^24"}],"packageManager":{"name":"pnpm","version":null},"future":{"keep":true}}}`))
	if err != nil {
		t.Fatal(err)
	}
	r := p.EngineDependencies("runtime")
	if len(r) != 2 || *r[0].Version != "^22" || *r[0].OnFail != "download" || *r[1].Version != "^24" {
		t.Fatal(r)
	}
	if pm := p.EngineDependencies("packageManager"); len(pm) != 1 || pm[0].Name != "pnpm" || pm[0].Version != nil {
		t.Fatal(pm)
	}
	if p.Raw.Get("devEngines").Get("future") == nil {
		t.Fatal("lost extra field")
	}
}
func TestOverrideSiblingReferences(t *testing.T) {
	p, _ := ParsePackage([]byte(`{"dependencies":{"a":"^1","indirect":"$a"},"devDependencies":{"a":"^2","b":"3"},"optionalDependencies":{"c":"4"},"peerDependencies":{"peer":"5"}}`))
	values := map[string]string{"x": "$a", "b": "$b", "c": "$c", "chain": "$indirect", "missing": "$peer", "literal": "1"}
	if missing := p.ResolveOverrideRefs(values); !reflect.DeepEqual(missing, []string{"missing"}) {
		t.Fatal(missing)
	}
	want := map[string]string{"x": "^1", "b": "3", "c": "4", "chain": "$a", "literal": "1"}
	if !reflect.DeepEqual(values, want) {
		t.Fatal(values)
	}
}
