package installdelta

import "github.com/nubjs/nub/pm-go/internal/lockfile"

type Plan struct {
	Added, Removed, Changed []string
	touch                   lockfile.Set
}

func (p Plan) Touched() int                { return len(p.Added) + len(p.Removed) + len(p.Changed) }
func (p Plan) Empty() bool                 { return p.Touched() == 0 }
func (p Plan) ShouldTouch(key string) bool { return p.touch.Has(key) }
func (p Plan) TouchedSet() lockfile.Set {
	out := lockfile.Set{}
	for key := range p.touch {
		out.Add(key)
	}
	return out
}
func Diff(stored, current map[string]string) Plan {
	p := Plan{Added: []string{}, Removed: []string{}, Changed: []string{}, touch: lockfile.Set{}}
	for _, key := range keys(current) {
		prior, ok := stored[key]
		if !ok {
			p.Added = append(p.Added, key)
			p.touch.Add(key)
		} else if prior != current[key] {
			p.Changed = append(p.Changed, key)
			p.touch.Add(key)
		}
	}
	for _, key := range keys(stored) {
		if _, ok := current[key]; !ok {
			p.Removed = append(p.Removed, key)
		}
	}
	return p
}
func ChangedSubtreeRoots(stored, current map[string]string) []string {
	changed := []string{}
	for _, key := range keys(current) {
		prior, ok := stored[key]
		if !ok || prior != current[key] {
			changed = append(changed, key)
		}
	}
	return changed
}
