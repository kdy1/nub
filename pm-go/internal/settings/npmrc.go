package settings

import (
	"slices"
	"strings"

	"github.com/nubjs/nub/pm-go/internal/npmconfig"
)

type NpmrcSources struct {
	User, Project []Entry
}

// LoadNpmrcSources shares file discovery with the registry client but keeps its
// settings view separate. Pnpm 11 filters scalar behavior keys in both scopes;
// Nub retains layout keys because branded YAML does not control its layout.
func LoadNpmrcSources(files npmconfig.Files) NpmrcSources {
	var out NpmrcSources
	for _, entry := range npmconfig.LoadFiles(files) {
		if files.Pnpm && files.Pnpm11 && !pnpm11Key(entry.Key) {
			continue
		}
		pair := Entry{entry.Key, entry.Value}
		switch entry.Source {
		case npmconfig.Builtin, npmconfig.Global, npmconfig.User, npmconfig.PnpmAuth, npmconfig.UserAuthFile:
			out.User = append(out.User, pair)
		case npmconfig.Project, npmconfig.ProjectAuthFile:
			out.Project = append(out.Project, pair)
		}
	}
	return out
}

// ExtendProject adds a more specific member's file sources after the root's.
// It retains the first load's user scope, as the engine's workspace path does.
func (s *NpmrcSources) ExtendProject(files npmconfig.Files) {
	s.Project = append(s.Project, LoadNpmrcSources(files).Project...)
}

func pnpm11Key(key string) bool {
	if strings.HasPrefix(key, "@") || strings.HasPrefix(key, "//") {
		return true
	}
	if slices.Contains([]string{"ca", "cafile", "cert", "key", "registry", "_auth", "_authToken", "_password", "email", "username", "https-proxy", "proxy", "no-proxy", "http-proxy", "local-address", "strict-ssl"}, key) {
		return true
	}
	for _, d := range definitions {
		if d.Layout && d.UnsupportedAdvice == "" && slices.Contains(d.Npmrc, key) {
			return true
		}
	}
	return false
}
