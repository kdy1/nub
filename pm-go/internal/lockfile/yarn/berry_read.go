package yarn

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/manifest"
	"go.yaml.in/yaml/v4"
)

func Read(path string, project *manifest.Package, options Options) (*lockfile.Graph, []Warning, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	return Parse(path, data, project, options)
}
func Parse(path string, data []byte, project *manifest.Package, options Options) (*lockfile.Graph, []Warning, error) {
	if IsBerry(string(data)) {
		return ParseBerry(path, data, project, options)
	}
	return ParseClassic(path, data, project, options)
}
func ParseBerry(path string, data []byte, project *manifest.Package, options Options) (*lockfile.Graph, []Warning, error) {
	doc, err := berryDocument(data)
	if err != nil {
		return nil, nil, err
	}
	version := uint64(0)
	if n := berryField(berryField(doc, "__metadata"), "version"); n != nil && n.Tag == "!!int" {
		_ = n.Decode(&version)
	}
	if version < 3 {
		return nil, nil, fmt.Errorf("yarn berry lockfile has unexpected __metadata.version: %d (expected >= 3)", version)
	}
	g := lockfile.NewGraph()
	g.PatchedDependencies = map[string]string{}
	g.SkippedOptionalDependencies = map[string]map[string]string{}
	bySpec := map[string]string{}
	unsupported := map[string]*UnsupportedSource{}
	var warnings []Warning
	for i := 0; i+1 < len(doc.Content); i += 2 {
		key := berryString(doc.Content[i])
		if key == nil || strings.HasPrefix(*key, "__") {
			continue
		}
		b := berryNode(doc.Content[i+1])
		if b == nil || b.Kind != yaml.MappingNode {
			return nil, warnings, fmt.Errorf("yarn berry block '%s' is not a mapping", *key)
		}
		specs := splitBerryHeader(*key)
		if len(specs) == 0 {
			continue
		}
		version := berryScalar(berryField(b, "version"))
		if version == nil {
			return nil, warnings, fmt.Errorf("yarn berry block '%s' has no version", *key)
		}
		resolution := berryString(berryField(b, "resolution"))
		if resolution == nil {
			return nil, warnings, fmt.Errorf("yarn berry block '%s' has no resolution", *key)
		}
		name, protocol, body, ok := parseBerrySpec(*resolution)
		if !ok {
			return nil, warnings, fmt.Errorf("yarn berry block '%s' has malformed resolution '%s'", *key, *resolution)
		}
		var source *lockfile.Source
		switch {
		case protocol == "npm":
		case protocol == "workspace":
			for _, s := range specs {
				bySpec[s] = name + "@" + *version
			}
			continue
		case protocol == "patch":
			patch, count := berryPatchPath(body)
			if count == 0 {
				warnings = append(warnings, Warning{"WARN_AUBE_YARN_BERRY_UNSUPPORTED", fmt.Sprintf("yarn berry patch protocol in block '%s' is not supported — entry skipped", *key)})
				continue
			}
			if count > 1 {
				return nil, warnings, fmt.Errorf("yarn berry patch block '%s' lists multiple patch files in one selector; aube applies one patch per package", *key)
			}
			g.PatchedDependencies[name+"@"+*version] = patch
		case protocol == "file":
			source = fileSource(body)
		case protocol == "portal":
			source = &lockfile.Source{Kind: lockfile.Portal, Path: stripHash(body)}
		case protocol == "link":
			source = &lockfile.Source{Kind: lockfile.Link, Path: stripHash(body)}
		case protocol == "exec":
			source = &lockfile.Source{Kind: lockfile.Exec, Path: stripHash(body)}
		case protocol == "http" || protocol == "https":
			url := protocol + ":" + body
			if strings.HasSuffix(url, ".git") || strings.Contains(url, ".git#") {
				source = berryGit(url)
			} else {
				source = &lockfile.Source{Kind: lockfile.RemoteTarball, URL: stripHash(url)}
			}
		case protocol == "git" || protocol == "ssh" || strings.HasPrefix(protocol, "git+") || strings.HasPrefix(protocol, "ssh+"):
			source = berryGit(protocol + ":" + body)
		default:
			if !options.AllowUnsupportedSources {
				for _, s := range specs {
					unsupported[s] = &UnsupportedSource{name, *resolution, protocol}
				}
			} else {
				warnings = append(warnings, Warning{"WARN_AUBE_YARN_BERRY_UNSUPPORTED", fmt.Sprintf("yarn berry unrecognized protocol '%s' in block '%s' — entry skipped", protocol, *key)})
			}
			continue
		}
		depPath := name + "@" + *version
		if source != nil {
			depPath = source.DepPath(name)
		}
		for _, s := range specs {
			bySpec[s] = depPath
			if base, _, ok := strings.Cut(s, "::"); ok && strings.Contains(base, "@patch:") {
				bySpec[base] = depPath
			}
		}
		if g.Packages[depPath] != nil {
			continue
		}
		p := lockfile.NewPackage(name, *version)
		p.DepPath, p.Source = depPath, source
		p.YarnChecksum = berryString(berryField(b, "checksum"))
		p.Dependencies = berryDependencies(berryField(b, "dependencies"))
		p.OptionalDependencies = berryDependencies(berryField(b, "optionalDependencies"))
		p.DeclaredDependencies = maps.Clone(p.Dependencies)
		maps.Copy(p.DeclaredDependencies, p.OptionalDependencies)
		for _, deps := range []map[string]string{p.Dependencies, p.OptionalDependencies} {
			for name, r := range deps {
				deps[name] = name + "@" + r
			}
		}
		p.PeerDependencies = berryDependencies(berryField(b, "peerDependencies"))
		if meta := berryField(b, "peerDependenciesMeta"); meta != nil && meta.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(meta.Content); i += 2 {
				name, m := berryString(meta.Content[i]), berryNode(meta.Content[i+1])
				if name == nil || m == nil || m.Kind != yaml.MappingNode {
					continue
				}
				optional := berryField(m, "optional")
				p.PeerDependenciesMeta[*name] = lockfile.PeerMeta{Optional: optional != nil && optional.Tag == "!!bool" && strings.EqualFold(optional.Value, "true")}
			}
		}
		g.Packages[depPath] = p
	}
	overrides := options.Overrides
	if overrides == nil {
		overrides = manifest.FlattenOverrides(project.Raw.Get("resolutions"), project.Raw.Get("pnpm").Get("overrides"), project.Raw.Get("aube").Get("overrides"), project.Raw.Get("overrides"))
	}
	rules := lockfile.CompileDirectOverrides(overrides)
	resolve := func(spec string, optional bool) (string, error) {
		if target, ok := bySpec[spec]; ok {
			return target, nil
		}
		if issue := unsupported[spec]; issue != nil {
			if !optional {
				return "", issue
			}
			warnings = append(warnings, Warning{"WARN_AUBE_LOCKFILE_UNSUPPORTED_SOURCE", fmt.Sprintf("optional dependency `%s` uses the unsupported `%s` source — skipping it (the install continues)", issue.Name, issue.Protocol)})
		}
		return "", nil
	}
	for _, key := range sortedKeys(g.Packages) {
		p := g.Packages[key]
		for index, deps := range []map[string]string{p.Dependencies, p.OptionalDependencies} {
			out := map[string]string{}
			for _, name := range sortedKeys(deps) {
				raw := deps[name]
				target, err := resolve(raw, index == 1)
				if err != nil {
					return nil, warnings, err
				}
				if target == "" {
					rangeText := strings.TrimPrefix(strings.TrimPrefix(raw, name+"@"), "npm:")
					if replacement := rules.Apply(name, rangeText); replacement != nil {
						for _, candidate := range berryCandidates(name, rangeText, replacement) {
							target, err = resolve(candidate, index == 1)
							if err != nil {
								return nil, warnings, err
							}
							if target != "" {
								break
							}
						}
					}
				}
				if target != "" {
					out[name] = target
				}
			}
			if index == 0 {
				p.Dependencies = out
			} else {
				p.OptionalDependencies = out
			}
		}
	}
	members, versions := berryMembers(filepath.Dir(path), project)
	members["."] = project
	for _, member := range sortedKeys(members) {
		pj := members[member]
		var direct []lockfile.DirectDep
		for index, deps := range []map[string]string{pj.Dependencies, pj.DevDependencies, pj.OptionalDependencies} {
			for _, name := range sortedKeys(deps) {
				rangeText, optional := deps[name], index == 2
				target, skipped := "", false
				for _, candidate := range berryCandidates(name, rangeText, rules.Apply(name, rangeText)) {
					target, err = resolve(candidate, optional)
					if err != nil {
						return nil, warnings, err
					}
					if target != "" {
						break
					}
					if optional && unsupported[candidate] != nil {
						if g.SkippedOptionalDependencies[member] == nil {
							g.SkippedOptionalDependencies[member] = map[string]string{}
						}
						g.SkippedOptionalDependencies[member][name] = rangeText
						skipped = true
						break
					}
				}
				if skipped {
					continue
				}
				if target == "" {
					target = berryWorkspaceLink(name, rangeText, versions)
				}
				if target != "" {
					direct = append(direct, lockfile.DirectDep{Name: name, DepPath: target, Type: lockfile.DepType(index)})
				}
			}
		}
		g.Importers[member] = direct
	}
	return g, warnings, nil
}
func berryMembers(dir string, project *manifest.Package) (map[string]*manifest.Package, map[string]string) {
	members, versions := map[string]*manifest.Package{}, map[string]string{}
	if project.Workspaces != nil {
		for _, member := range discoverMembers(dir, project.Workspaces.Patterns) {
			pj, err := manifest.ReadPackage(filepath.Join(dir, member, "package.json"))
			if err != nil {
				continue
			}
			members[member] = pj
			if pj.Name != nil {
				version := "0.0.0"
				if pj.Version != nil {
					version = *pj.Version
				}
				versions[*pj.Name] = version
			}
		}
	}
	return members, versions
}
func berryWorkspaceLink(name, rangeText string, versions map[string]string) string {
	version, ok := versions[name]
	if !ok {
		return ""
	}
	if strings.HasPrefix(rangeText, "link:") || strings.HasPrefix(rangeText, "portal:") {
		return name + "@" + version
	}
	if rest, ok := strings.CutPrefix(rangeText, "workspace:"); ok {
		if rest == "^" || rest == "~" {
			return name + "@" + version
		}
		rangeText = rest
	}
	if rangeText == "" || rangeText == "*" || versionSatisfies(version, rangeText) {
		return name + "@" + version
	}
	return ""
}
