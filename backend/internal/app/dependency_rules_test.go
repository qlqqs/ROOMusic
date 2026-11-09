package app

import (
	"go/build"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const internalPrefix = "github.com/qlqq/roomusic/backend/internal/"

// packageRule 规定一个 internal 包允许依赖的内部包（相对 internal/ 的路径），以及是否可以使用 net/http。
type packageRule struct {
	imports []string
	http    bool
}

// packageRules 是后端包的依赖方向合同（见 .trellis/spec/backend/directory-structure.md）：
// 平台包是叶子；能力核心（catalog、library、parser）不含 HTTP；跨能力只经公开合同；
// 各 httpapi 包只依赖本能力、identity 与 platform，且彼此不导入；只有 app 可以装配全部。
// 新增包必须先在这里声明规则，测试才会通过。
var packageRules = map[string]packageRule{
	"backup":                         {imports: []string{"platform/config", "platform/postgres"}},
	"backup/httpapi":                 {imports: []string{"backup", "identity", "platform/buildinfo", "platform/httpserver"}, http: true},
	"platform/buildinfo":             {},
	"platform/uuid":                  {},
	"platform/weborigin":             {},
	"platform/workpool":              {},
	"platform/config":                {imports: []string{"platform/weborigin"}},
	"platform/httpserver":            {http: true},
	"platform/postgres":              {},
	"platform/postgres/postgrestest": {imports: []string{"platform/uuid"}},
	"catalog/internal/releasesql":    {imports: []string{"catalog/internal/artistgraph", "catalog/textnorm"}},
	"catalog/classical":              {},
	"catalog/metadata":               {},
	"catalog/textnorm":               {},
	"catalog/internal/artistgraph":   {imports: []string{"catalog/textnorm", "catalog/metadata", "catalog/internal/searchindex", "platform/uuid"}},
	"catalog/internal/writegate":     {},
	"catalog/internal/searchindex":   {imports: []string{"catalog/textnorm", "catalog/metadata", "catalog/internal/releasesql"}},
	"catalog/testsupport":            {imports: []string{"catalog/internal/artistgraph", "catalog/internal/searchindex", "catalog/textnorm"}},
	"catalog/maintenance":            {imports: []string{"catalog/internal/artistgraph", "catalog/textnorm", "catalog/internal/searchindex", "catalog/internal/writegate"}},
	"catalog/artwork":                {},
	"catalog/ingest":                 {imports: []string{"catalog/classical", "catalog/internal/artistgraph", "catalog/metadata", "platform/uuid", "platform/workpool", "catalog/internal/searchindex", "catalog/internal/writegate"}},
	"catalog":                        {imports: []string{"catalog/internal/artistgraph", "catalog/internal/writegate", "catalog/internal/releasesql", "platform/uuid", "catalog/textnorm", "catalog/internal/searchindex"}},
	"catalog/governance":             {imports: []string{"catalog/textnorm", "catalog/internal/artistgraph", "catalog", "catalog/internal/releasesql", "catalog/metadata", "operations", "platform/uuid", "catalog/internal/searchindex", "catalog/internal/writegate"}},
	"operations":                     {imports: []string{"platform/uuid"}},
	"operations/httpapi":             {imports: []string{"identity", "operations", "platform/httpserver", "platform/uuid"}, http: true},
	"library/parser":                 {imports: []string{"catalog/metadata", "catalog/classical"}},
	"library/parser/parsertest":      {},
	"library":                        {imports: []string{"catalog", "catalog/governance", "catalog/artwork", "catalog/ingest", "catalog/metadata", "library/parser", "platform/uuid", "platform/workpool"}},
	"playback/analysis":              {},
	"playback/derivation":            {},
	"playback":                       {imports: []string{"catalog", "library", "playback/analysis", "playback/derivation"}},
	"zones/queue":                    {},
	"zones":                          {imports: []string{"catalog", "platform/uuid", "zones/queue"}},
	"listening":                      {imports: []string{"catalog"}},
	"personal":                       {imports: []string{"catalog", "platform/uuid"}},
	"personal/httpapi":               {imports: []string{"identity", "personal", "platform/httpserver", "platform/uuid"}, http: true},
	"identity":                       {imports: []string{"platform/httpserver", "platform/weborigin"}, http: true},
	"identity/httpapi":               {imports: []string{"identity", "platform/httpserver", "platform/uuid"}, http: true},
	"catalog/httpapi":                {imports: []string{"catalog", "catalog/artwork", "catalog/governance", "identity", "operations", "platform/httpserver", "platform/uuid"}, http: true},
	"library/httpapi":                {imports: []string{"identity", "library", "platform/httpserver", "platform/uuid"}, http: true},
	"playback/httpapi":               {imports: []string{"identity", "platform/httpserver", "platform/uuid", "playback"}, http: true},
	"listening/httpapi":              {imports: []string{"identity", "listening", "platform/httpserver", "platform/uuid"}, http: true},
	"app": {imports: []string{
		"backup", "backup/httpapi", "zones", "zones/queue", "zones/httpapi",
		"catalog", "catalog/governance", "catalog/httpapi", "catalog/maintenance", "identity", "identity/httpapi", "library", "library/httpapi", "listening", "listening/httpapi",
		"personal", "personal/httpapi", "operations/httpapi", "platform/config", "platform/httpserver", "playback", "playback/analysis", "playback/derivation", "playback/httpapi",
	}, http: true},
}

func TestPackageDependencyDirections(t *testing.T) {
	internalDirectory := filepath.Join("..")
	found := map[string]bool{}
	err := filepath.WalkDir(internalDirectory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || !entry.IsDir() {
			return walkErr
		}
		pkg, importErr := build.ImportDir(path, 0)
		if importErr != nil {
			if _, none := importErr.(*build.NoGoError); none {
				return nil
			}
			return importErr
		}
		relative := filepath.ToSlash(strings.TrimPrefix(strings.TrimPrefix(path, internalDirectory), string(filepath.Separator)))
		rule, declared := packageRules[relative]
		if !declared {
			t.Errorf("package internal/%s has no dependency rule; declare it in packageRules", relative)
			return nil
		}
		found[relative] = true
		allowed := map[string]bool{}
		for _, item := range rule.imports {
			allowed[item] = true
		}
		for _, imported := range pkg.Imports {
			if imported == "net/http" && !rule.http {
				t.Errorf("internal/%s must not import net/http", relative)
			}
			if strings.HasPrefix(imported, internalPrefix) {
				name := strings.TrimPrefix(imported, internalPrefix)
				if !allowed[name] {
					t.Errorf("internal/%s must not import internal/%s", relative, name)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal packages: %v", err)
	}
	var missing []string
	for name := range packageRules {
		if !found[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("dependency rules for packages that no longer exist: %v", missing)
	}
}
