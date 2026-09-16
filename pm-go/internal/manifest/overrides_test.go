package manifest

import (
	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
	"reflect"
	"testing"
)

func TestFlattenSelectedOverrideSources(t *testing.T) {
	v, err := jsonvalue.Parse([]byte(`{"resolutions":{"foo":"1","untouched":"1"},"pnpm":{"overrides":{"foo":"private"}},"overrides":{"foo":"2","parent@2":{".":"3","child":{".":"4","@scope/leaf":"npm:other@5"},"ignored":true},"": "ignored", "bad":{".":{ "child":"ignored"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	got := FlattenOverrides(v.Get("resolutions"), v.Get("overrides"))
	want := map[string]string{"foo": "2", "untouched": "1", "parent@2": "3", "parent@2>child": "4", "parent@2>child>@scope/leaf": "npm:other@5"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
	if !reflect.DeepEqual(FlattenOverrides(v.Get("pnpm").Get("overrides")), map[string]string{"foo": "private"}) {
		t.Fatal("source selection changed")
	}
}
