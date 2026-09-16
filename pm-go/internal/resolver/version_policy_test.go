package resolver

import "testing"

func TestVersionPolicyDefaultsLossyAndNames(t *testing.T) {
	p, errs := ParseVersionPolicyLossy([]string{"", "pkg@^1", "@scope/*", "bad@invalid", "glob*@1", "pkg@1 ||", " whitespace "})
	if p.Len() != 3 || len(errs) != 3 {
		t.Fatal(p, errs)
	}
	if !p.Excludes("pkg", "1.2.3") || p.Excludes("pkg", "2.0.0") || !p.Excludes("@scope/name", "invalid") || p.Excludes("whitespace", "1.0.0") {
		t.Fatal(p)
	}
	if !p.HasVersionedMatch("pkg") || p.MatchesNameOnly("pkg") || !p.MatchesNameOnly("@scope/name") {
		t.Fatal(p)
	}
	if (PackageVersionPolicy{}).Excludes("semver", "7.0.0") {
		t.Fatal("age policy inherits trust defaults")
	}
	defaults := TrustExcludesWithUserRules(p)
	if !defaults.Excludes("semver", "7.0.0") || !defaults.Excludes("@hono/node-server", "1.19.15") || defaults.Excludes("@hono/node-server", "1.19.16") || !defaults.Excludes("pkg", "1.0.0") {
		t.Fatal(defaults)
	}
}
func TestRustVersionPolicyOracle(t *testing.T) {
	type task struct{ Name, Version string }
	type input struct {
		Patterns []string
		Default  bool
		Tasks    []task
	}
	var tasks []task
	for _, name := range []string{"pkg", "pkg-suffix", "prefix-pkg", "@scope/pkg", "@scope/other", "foo[bar]", " ", "semver", "@hono/node-server", "@octokit/endpoint", ""} {
		for _, version := range []string{"1.0.0", "1.2.3", "1.19.15", "1.19.16", "2.0.0", "1.0.0-beta.1", "01.02.03", "invalid"} {
			tasks = append(tasks, task{name, version})
		}
	}
	inputs := []input{{Default: true, Tasks: tasks}}
	for _, pattern := range []string{"", "pkg", "pkg@^1", "pkg@1.0.0 || 2.0.0", "pkg@<=1.2", "@scope/pkg@>=1 <2", "@scope/*", "*pkg", "pkg*", "**", "*pkg*", "p*g", "foo[bar]", " ", "pkg@", "pkg@1 ||", "pkg@invalid", "pkg*@1", "@scope/*@1", "pkg@1.2.3 garbage"} {
		inputs = append(inputs, input{Patterns: []string{pattern}, Tasks: tasks})
	}
	var refs []struct {
		Len     int
		Matches []bool
		Error   *string
	}
	runPolicyOracle(t, "version-policy", inputs, &refs)
	if len(refs) != len(inputs) {
		t.Fatal("result count")
	}
	for i, in := range inputs {
		p, err := ParseVersionPolicy(in.Patterns)
		if in.Default {
			p = DefaultTrustExcludes()
		}
		if refs[i].Error != nil {
			if err == nil || err.Error() != *refs[i].Error {
				t.Fatal(i, err, refs[i].Error)
			}
			continue
		}
		if err != nil || p.Len() != refs[i].Len || len(refs[i].Matches) != len(tasks) {
			t.Fatal(i, err, p.Len(), refs[i])
		}
		for j, task := range tasks {
			if p.Excludes(task.Name, task.Version) != refs[i].Matches[j] {
				t.Fatal(i, task)
			}
		}
	}
	t.Logf("compared %d package-version policies against %d coordinates", len(inputs), len(tasks))
}
