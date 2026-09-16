package bun

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/fsutil"
	"github.com/nubjs/nub/pm-go/internal/jsonvalue"
	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/manifest"
)

func Write(path string, graph *lockfile.Graph, project *manifest.Package) error {
	data, err := Encode(path, graph, project)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return fsutil.WriteDefault(path, data)
}
func Encode(path string, graph *lockfile.Graph, project *manifest.Package) ([]byte, error) {
	canonical := map[string]*lockfile.Package{}
	for _, key := range sortedKeys(graph.Packages) {
		p := graph.Packages[key]
		if p.Source != nil && p.Source.Kind == lockfile.Link {
			continue
		}
		for _, key := range []string{p.SpecKey(), lockfile.CanonicalKey(p.DepPath)} {
			if canonical[key] == nil {
				canonical[key] = p
			}
		}
	}
	var roots []lockfile.DirectDep
	seen := lockfile.Set{}
	for _, importer := range sortedKeys(graph.Importers) {
		for _, d := range graph.Importers[importer] {
			if p := graph.Packages[d.DepPath]; p != nil && p.Source != nil && p.Source.Kind == lockfile.Link {
				continue
			}
			if seen.Add(d.Name) {
				roots = append(roots, d)
			}
		}
	}
	workspaces := []jsonvalue.Field{{Key: "", Value: workspaceObject(project, true, graph.WorkspaceExtraFields["."])}}
	for _, importer := range sortedKeys(graph.Importers) {
		if importer == "." {
			continue
		}
		pj, err := manifest.ReadPackage(filepath.Join(filepath.Dir(path), importer, "package.json"))
		if err != nil {
			pj = &manifest.Package{}
		}
		workspaces = append(workspaces, jsonvalue.Field{Key: importer, Value: workspaceObject(pj, false, graph.WorkspaceExtraFields[importer])})
	}
	var packages []jsonvalue.Field
	for _, place := range lockfile.HoistTree(canonical, roots, nil) {
		p := canonical[place.Key]
		if p == nil {
			continue
		}
		meta := dependencyMetadata(p, func(key string) bool { return canonical[key] != nil })
		if len(p.PeerDependencies) != 0 {
			meta.Put("peerDependencies", stringsObject(p.PeerDependencies))
		}
		var optionalPeers []string
		for _, name := range sortedKeys(p.PeerDependenciesMeta) {
			if p.PeerDependenciesMeta[name].Optional {
				optionalPeers = append(optionalPeers, name)
			}
		}
		if len(optionalPeers) != 0 {
			meta.Put("optionalPeers", stringArray(optionalPeers))
		}
		for _, field := range []struct {
			key    string
			values []string
		}{{"os", p.OS}, {"cpu", p.CPU}, {"libc", p.Libc}} {
			if len(field.values) != 0 {
				meta.Put(field.key, stringArray(field.values))
			}
		}
		if bin := p.ExtraMeta["bin"]; bin != nil && bin.Kind != 'n' {
			meta.Put("bin", bin.Clone())
		} else {
			bins := map[string]string{}
			for name, path := range p.Bin {
				if name != "" {
					bins[name] = path
				}
			}
			if len(bins) != 0 {
				meta.Put("bin", stringsObject(bins))
			}
		}
		for _, key := range sortedKeys(p.ExtraMeta) {
			if slices.Contains([]string{"dependencies", "optionalDependencies", "peerDependencies", "optionalPeers", "bin", "os", "cpu", "libc", "__aube_preserve_tarball_url"}, key) {
				continue
			}
			meta.Put(key, p.ExtraMeta[key].Clone())
		}
		packages = append(packages, jsonvalue.Field{Key: strings.Join(place.Segments, "/"), Value: packageTuple(p, meta)})
	}
	workspacePaths := lockfile.Set{}
	for _, p := range graph.Packages {
		if p.Source != nil && p.Source.Kind == lockfile.Link {
			workspacePaths.Add(p.DepPath)
		}
	}
	emitted := lockfile.Set{}
	for _, key := range sortedKeys(graph.Packages) {
		p := graph.Packages[key]
		if p.Source == nil || p.Source.Kind != lockfile.Link {
			continue
		}
		name := p.RegistryName()
		if !emitted.Add(name) {
			continue
		}
		spec, ok := strings.CutPrefix(p.Version, "workspace:")
		if !ok {
			spec = p.Source.Path
			if spec == "" || spec == "." {
				spec = "*"
			}
		}
		meta := dependencyMetadata(p, func(key string) bool { return canonical[key] != nil || workspacePaths.Has(key) })
		tuple := &jsonvalue.Value{Kind: '[', Array: []*jsonvalue.Value{jsonvalue.String(name + "@workspace:" + spec)}}
		if len(meta.Object) != 0 {
			tuple.Array = append(tuple.Array, meta)
		}
		packages = append(packages, jsonvalue.Field{Key: name, Value: tuple})
	}
	slices.SortStableFunc(packages, func(a, b jsonvalue.Field) int { return strings.Compare(a.Key, b.Key) })
	extras := jsonvalue.Object()
	if len(graph.TrustedDependencies) != 0 {
		extras.Put("trustedDependencies", stringArray(graph.TrustedDependencies))
	}
	if len(graph.PatchedDependencies) != 0 {
		extras.Put("patchedDependencies", stringsObject(graph.PatchedDependencies))
	}
	if len(graph.Overrides) != 0 {
		extras.Put("overrides", stringsObject(graph.Overrides))
	}
	catalogs := jsonvalue.Object()
	for _, name := range sortedKeys(graph.Catalogs) {
		values := map[string]string{}
		for key, v := range graph.Catalogs[name] {
			values[key] = v.Specifier
		}
		if name == "default" {
			if len(values) != 0 {
				extras.Put("catalog", stringsObject(values))
			}
		} else {
			catalogs.Put(name, stringsObject(values))
		}
	}
	if len(catalogs.Object) != 0 {
		extras.Put("catalogs", catalogs)
	}
	for _, key := range sortedKeys(graph.ExtraFields) {
		if slices.Contains([]string{"lockfileVersion", "configVersion", "workspaces", "packages", "overrides", "patchedDependencies", "trustedDependencies", "catalog", "catalogs"}, key) {
			continue
		}
		extras.Put(key, graph.ExtraFields[key].Clone())
	}
	config := uint32(1)
	if graph.BunConfigVersion != nil {
		config = *graph.BunConfigVersion
	}
	return formatLockfile(workspaces, packages, config, extras.Object), nil
}

func stringsObject(values map[string]string) *jsonvalue.Value {
	v := jsonvalue.Object()
	for _, key := range sortedKeys(values) {
		v.Put(key, jsonvalue.String(values[key]))
	}
	return v
}
func stringArray(values []string) *jsonvalue.Value {
	v := &jsonvalue.Value{Kind: '['}
	for _, s := range values {
		v.Array = append(v.Array, jsonvalue.String(s))
	}
	return v
}
func workspaceObject(pj *manifest.Package, root bool, extras map[string]*jsonvalue.Value) *jsonvalue.Value {
	v := jsonvalue.Object()
	if pj.Name != nil {
		v.Put("name", jsonvalue.String(*pj.Name))
	}
	if !root {
		if pj.Version != nil {
			v.Put("version", jsonvalue.String(*pj.Version))
		}
		if bin := pj.Raw.Get("bin"); bin != nil {
			v.Put("bin", bin.Clone())
		}
	}
	for _, field := range []struct {
		key    string
		values map[string]string
	}{{"dependencies", pj.Dependencies}, {"devDependencies", pj.DevDependencies}, {"optionalDependencies", pj.OptionalDependencies}, {"peerDependencies", pj.PeerDependencies}} {
		if len(field.values) != 0 {
			v.Put(field.key, stringsObject(field.values))
		}
	}
	if !root {
		if meta := pj.Raw.Get("peerDependenciesMeta"); meta != nil && meta.Kind == '{' {
			var peers []string
			for _, f := range meta.Object {
				if optional := f.Value.Get("optional"); optional != nil && optional.Kind == 'b' && optional.Scalar == true {
					peers = append(peers, f.Key)
				}
			}
			slices.Sort(peers)
			if len(peers) != 0 {
				v.Put("optionalPeers", stringArray(peers))
			}
		}
	}
	for _, key := range sortedKeys(extras) {
		if v.Get(key) == nil {
			v.Put(key, extras[key].Clone())
		}
	}
	return v
}
func dependencyMetadata(p *lockfile.Package, contains func(string) bool) *jsonvalue.Value {
	deps, opts := jsonvalue.Object(), jsonvalue.Object()
	for _, name := range sortedKeys(p.Dependencies) {
		tail := p.Dependencies[name]
		if !contains(lockfile.ChildCanonicalKey(name, tail)) {
			continue
		}
		rendered, ok := p.DeclaredDependencies[name]
		if !ok {
			rendered = lockfile.DependencyVersion(name, tail)
		}
		if _, optional := p.OptionalDependencies[name]; optional {
			opts.Put(name, jsonvalue.String(rendered))
		} else {
			deps.Put(name, jsonvalue.String(rendered))
		}
	}
	meta := jsonvalue.Object()
	if len(deps.Object) != 0 {
		meta.Put("dependencies", deps)
	}
	if len(opts.Object) != 0 {
		meta.Put("optionalDependencies", opts)
	}
	return meta
}
func packageTuple(p *lockfile.Package, meta *jsonvalue.Value) *jsonvalue.Value {
	name := p.RegistryName()
	var git *lockfile.Source
	if p.Source != nil {
		if p.Source.Kind == lockfile.Git {
			git = p.Source
		} else if hosted, ok := p.Source.HostedGitSource(); ok {
			git = hosted
		}
	}
	v := &jsonvalue.Value{Kind: '['}
	if git == nil {
		integrity, registry := "", ""
		if p.Integrity != nil {
			integrity = *p.Integrity
		}
		if p.TarballURL != nil {
			registry = *p.TarballURL
		}
		v.Array = []*jsonvalue.Value{jsonvalue.String(name + "@" + p.Version), jsonvalue.String(registry), meta, jsonvalue.String(integrity)}
		return v
	}
	originalGit := strings.HasPrefix(p.Version, "github:") || strings.HasPrefix(p.Version, "git+") || strings.HasPrefix(p.Version, "git://") || strings.HasPrefix(p.Version, "git@")
	commit := git.Resolved
	if commit == "" && git.Committish != nil {
		commit = *git.Committish
	}
	if len(commit) == 40 && strings.Trim(commit, "0123456789abcdefABCDEF") == "" {
		commit = commit[:7]
	}
	tail := p.Version
	hosted, isHosted := lockfile.ParseHostedGit(git.URL)
	if !originalGit {
		if isHosted {
			protocol := map[string]string{"github.com": "github", "gitlab.com": "gitlab", "bitbucket.org": "bitbucket"}[hosted.Host]
			tail = protocol + ":" + hosted.Owner + "/" + hosted.Repo + "#" + commit
		} else if strings.HasPrefix(git.URL, "git://") || strings.HasPrefix(git.URL, "git+") {
			tail = git.URL + "#" + commit
		} else {
			tail = "git+" + git.URL + "#" + commit
		}
	}
	tagCommit := commit
	if i := strings.LastIndexByte(tail, '#'); i >= 0 {
		tagCommit = tail[i+1:]
	}
	var tag string
	if isHosted {
		tag = hosted.Owner + "-" + hosted.Repo + "-" + tagCommit
	} else {
		stem := strings.TrimRight(git.URL, "/")
		if i := strings.LastIndexByte(stem, '/'); i >= 0 {
			stem = stem[i+1:]
		}
		for strings.HasSuffix(stem, ".git") {
			stem = strings.TrimSuffix(stem, ".git")
		}
		tag = stem + "-" + tagCommit
	}
	v.Array = []*jsonvalue.Value{jsonvalue.String(name + "@" + tail), meta, jsonvalue.String(tag)}
	if originalGit && p.Integrity != nil && *p.Integrity != "" {
		v.Array = append(v.Array, jsonvalue.String(*p.Integrity))
	}
	return v
}
