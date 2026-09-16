package npmconfig

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParserTrustAndPhysicalLines(t *testing.T) {
	data := "\ufeff ;comment\r\nregistry = '${HOST}/'\r\n//${HOST}/:_authToken=abc\\\r\ndef=ghi\r\nca[]=\"line\\nnext\"\nflag\nquoted='x#y;z'\nmissing=${ABSENT}\nunterminated=${ABSENT"
	env := map[string]string{"HOST": "registry.example"}
	trusted := Parse(data, User, env)
	want := []Entry{{User, "registry", "registry.example/"}, {User, "//registry.example/:_authToken", "abcdef=ghi"}, {User, "ca[]", `line\nnext`}, {User, "quoted", "x#y;z"}, {User, "missing", ""}, {User, "unterminated", ""}}
	if !reflect.DeepEqual(trusted, want) {
		t.Fatalf("%#v", trusted)
	}
	untrusted := Parse(data, Project, env)
	if untrusted[0].Value != "${HOST}/" || untrusted[1].Key != "//${HOST}/:_authToken" {
		t.Fatalf("project expanded environment: %#v", untrusted)
	}
	if Expand("${A}", map[string]string{"A": "${B}", "B": "secret"}) != "${B}" {
		t.Fatal("recursive expansion")
	}
}

func TestCredentialsStayAtTheirSourceRegistry(t *testing.T) {
	entries := append(Parse("_authToken=private-token\nregistry=https://private.example/", User, nil), Parse("registry=https://project.example/\n//project.example/:_authToken=${SECRET}\n//${HOST}/:_authToken=literal", Project, nil)...)
	c := Resolve(entries, nil)
	if c.Registry != "https://project.example/" {
		t.Fatal(c.Registry)
	}
	if a := c.AuthFor("https://private.example/path/tarball.tgz", "a"); a == nil || *a.Token != "private-token" {
		t.Fatalf("%+v", a)
	}
	if c.AuthFor(c.Registry, "a") != nil {
		t.Fatal("project captured user credentials")
	}
	if len(c.Warnings) != 3 {
		t.Fatalf("%+v", c.Warnings)
	}
	for _, w := range c.Warnings {
		if strings.Contains(w.Message, "private-token") {
			t.Fatal("warning disclosed credential")
		}
	}
	// A later unscoped entry must not replace explicitly scoped auth, even
	// when its source has higher normal config precedence.
	c = Resolve([]Entry{{User, "//registry.npmjs.org/:_authToken", "scoped"}, {Env, "_authToken", "unscoped"}}, nil)
	if got := *c.AuthFor(DefaultRegistry, "a").Token; got != "scoped" {
		t.Fatal(got)
	}
}

func TestAuthLongestPathAndPackageScope(t *testing.T) {
	c := Resolve(Parse("//example/:_authToken=root\n//example/a/:_authToken=path\n//example/a/:@ORG:_authToken=scoped\n//example/a/b/:@org:ca=pem\n//other/:_authToken=other", User, nil), nil)
	for _, tc := range []struct{ url, name, want string }{
		{"https://example/", "x", "root"}, {"https://example/a/file", "x", "path"}, {"https://example/ab/file", "x", "root"}, {"https://example/a/b/file", "@org/pkg", "scoped"}, {"https://other/", "@org/pkg", "other"},
	} {
		if a := c.AuthFor(tc.url, tc.name); a == nil || a.Token == nil || *a.Token != tc.want {
			t.Fatalf("%+v: %+v", tc, a)
		}
	}
	if c.AuthFor("https://example.evil/", "x") != nil || c.AuthFor("https://example:444/", "x") != nil {
		t.Fatal("credential authority boundary")
	}
	if tls := c.ScopedTLSFor("https://example/a/b/file", "@org/pkg"); tls == nil || !reflect.DeepEqual(tls.CA, []string{"pem"}) {
		t.Fatalf("%+v", tls)
	}
}

func TestURIKeyPorts(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"https://host:443/a/", "//host/a/"}, {"http://host:80/", "//host/"}, {"https://host:80/", "//host:80/"}, {"http://host:443/", "//host:443/"}, {"https://[::1]:443/x", "//[::1]/x"},
	} {
		if got := URIKey(tc.in); got != tc.want {
			t.Fatal(tc, got)
		}
	}
	c := Resolve(Parse("//host:443/:_authToken=x", User, nil), nil)
	if c.AuthFor("https://host/", "x") == nil || c.AuthFor("http://host:443/", "x") != nil {
		t.Fatal("wrong default-port collapse")
	}
}

func TestTLSProxyAndHelperSourceGates(t *testing.T) {
	for _, source := range []Source{Project, ProjectAuthFile} {
		c := Resolve(Parse("strict-ssl=false\nhttps-proxy=http://bad\ntokenHelper=/bad\n//host/:tokenHelper=/bad\nca[]=first\\nline\nca[]=second", source, nil), nil)
		if !c.StrictSSL || c.HTTPSProxy != nil || len(c.Auth) != 0 || len(c.Warnings) != 4 {
			t.Fatalf("%s: %+v", source, c)
		}
		if !reflect.DeepEqual(c.TLS.CA, []string{"first\nline", "second"}) {
			t.Fatal(c.TLS)
		}
	}
	c := Resolve(Parse("strict-ssl=false\nhttps-proxy=http://configured\ntokenHelper=/usr/bin/helper", User, nil), map[string]string{"HTTPS_PROXY": "http://environment", "HTTP_PROXY": "http://http"})
	if c.StrictSSL || *c.HTTPSProxy != "http://configured" || *c.HTTPProxy != "http://configured" || c.AuthFor(DefaultRegistry, "").TokenHelper == nil {
		t.Fatalf("%+v", c)
	}
	c = Resolve(Parse("proxy=false", User, nil), map[string]string{"HTTPS_PROXY": "http://environment", "HTTP_PROXY": "http://http"})
	if c.HTTPSProxy != nil || c.HTTPProxy != nil {
		t.Fatal("proxy=false ignored")
	}
	for _, path := range []string{"/bin/helper", `C:\bin\helper.exe`, `\\server\share\helper.exe`} {
		if !ValidTokenHelper(path) {
			t.Fatal(path)
		}
	}
	for _, path := range []string{"helper", "/bin/helper --arg", "/tmp/helper;id", "${HELPER}", "/tmp/a\x00b", `C:relative`} {
		if ValidTokenHelper(path) {
			t.Fatal(path)
		}
	}
}

func TestBasicAuthAndAlwaysAuth(t *testing.T) {
	c := Resolve(Parse("//host/:username=user\n//host/:_password=cGFzcw==\n//host/:always-auth=yes", User, nil), nil)
	a := c.AuthFor("https://host/path", "pkg")
	value, ok := a.BasicValue()
	if !ok || value != base64.StdEncoding.EncodeToString([]byte("user:pass")) || !c.AlwaysAuthFor("https://host/path") || c.AlwaysAuthFor("https://other/") {
		t.Fatal(value, ok)
	}
	a.Password = new("not base64")
	if _, ok := a.BasicValue(); ok {
		t.Fatal("invalid password accepted")
	}
}

func TestEnvironmentIncumbentAndPrecedence(t *testing.T) {
	vars := []string{"PNPM_CONFIG_REGISTRY=https://pnpm/", "npm_config_registry=https://npm/", "pnpm_config_strictSsl=false", "pnpm_config_strict__ssl=false", "pnpm_config_strict_ssl=false", "NPM_CONFIG_@ORG:REGISTRY=https://org/", "pnpm_config_//host/:_authToken=pnpm", "npm_config_//host/:_authToken=npm", "BUN_CONFIG_REGISTRY=https://bun/", "BUN_CONFIG_TOKEN=bun-token"}
	for _, tc := range []struct {
		pnpm, bun bool
		registry  string
		strict    bool
	}{{false, false, "https://npm/", true}, {true, false, "https://pnpm/", false}, {false, true, "https://bun/", true}} {
		c := Resolve(EnvEntries(vars, tc.pnpm, tc.bun), nil)
		if c.Registry != tc.registry || c.StrictSSL != tc.strict || c.RegistryFor("@org/pkg") != "https://org/" || *c.AuthFor("https://host/", "x").Token != "pnpm" {
			t.Fatalf("%+v: %+v", tc, c)
		}
		if tc.bun && *c.AuthFor(c.Registry, "x").Token != "bun-token" {
			t.Fatal("Bun token not pinned")
		}
	}
}

func TestFileCascadeAndAuthSidecarTrust(t *testing.T) {
	root := t.TempDir()
	home, project := filepath.Join(root, "home"), filepath.Join(root, "project")
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(home, ".npmrc"), "registry=https://user/\n_authToken=${TOKEN}\n")
	write(filepath.Join(project, ".npmrc"), "registry=https://project/\nnpmrc-auth-file=./auth\n")
	write(filepath.Join(project, "auth"), "//project/:_authToken=${TOKEN}\n//project/:tokenHelper=/tmp/helper\n")
	write(filepath.Join(root, "xdg", "pnpm", "auth.ini"), "//pnpm/:_authToken=pnpm")
	f := Files{Dir: project, Home: home, Env: map[string]string{"TOKEN": "secret", "XDG_CONFIG_HOME": filepath.Join(root, "xdg")}}
	for _, pnpm11 := range []bool{false, true} {
		f.Pnpm, f.Pnpm11 = pnpm11, pnpm11
		c := Resolve(LoadFiles(f), nil)
		if c.AuthFor("https://project/", "x") != nil || *c.AuthFor("https://user/", "x").Token != "secret" {
			t.Fatal("sidecar gained user trust")
		}
		if (c.AuthFor("https://pnpm/", "x") != nil) != pnpm11 {
			t.Fatal("pnpm auth.ini identity gate")
		}
	}
	f.Dir = home
	c := Resolve(LoadFiles(f), nil)
	if len(c.Warnings) != 1 {
		t.Fatalf("user file read twice: %+v", c.Warnings)
	}
	write(filepath.Join(root, "override"), "registry=https://override/")
	f.Env["PNPM_CONFIG_USERCONFIG"] = filepath.Join(root, "override")
	f.Pnpm = false
	if UserFile(f) != filepath.Join(home, ".npmrc") {
		t.Fatal("foreign userconfig read")
	}
	f.Pnpm = true
	if UserFile(f) != filepath.Join(root, "override") {
		t.Fatal("pnpm userconfig ignored")
	}
}
