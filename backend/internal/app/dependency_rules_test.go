package app

import (
	"go/build"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const (
	backendPrefix  = "github.com/qlqq/roomusic/backend/"
	internalPrefix = backendPrefix + "internal/"
)

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
	"insights":                          {imports: []string{"insights/schema", "platform/egress", "sources"}, http: true},
	"insights/httpapi":                  {imports: []string{"identity", "insights", "platform/httpserver"}, http: true},
	"insights/schema":                   {imports: []string{"platform/uuid", "platform/buildinfo"}},
	"plugins/runtime":                   {},
	"plugins":                           {imports: []string{"plugins/runtime", "sources"}},
	"plugins/httpapi":                   {imports: []string{"identity", "platform/httpserver", "plugins"}, http: true},
	"platform/diagnostics":              {imports: []string{"platform/buildinfo", "platform/postgres"}},
	"platform/diagnostics/httpapi":      {imports: []string{"identity", "platform/diagnostics", "platform/httpserver"}, http: true},
	"platform/diagnostics/stewardtools": {imports: []string{"platform/diagnostics", "steward/tool"}},
	"backup/stewardtools":               {imports: []string{"backup", "steward/tool"}},
	"identity/stewardtools":             {imports: []string{"identity", "platform/uuid", "steward/tool"}},
	"playback/stewardtools":             {imports: []string{"catalog", "platform/uuid", "playback", "playback/analysis", "steward/tool"}},
	"dspsettings/stewardtools":          {imports: []string{"dspsettings", "platform/uuid", "steward/tool", "zones"}},
	"zones/stewardtools":                {imports: []string{"catalog", "zones", "zones/queue", "platform/uuid", "steward/tool"}},
	"listening/stewardtools":            {imports: []string{"listening", "platform/uuid", "steward/tool"}},
	"personal/stewardtools":             {imports: []string{"personal", "platform/uuid", "steward/tool"}},
	"catalog/governance/stewardtools":   {imports: []string{"catalog", "catalog/governance", "operations", "platform/uuid", "steward/tool"}},
	"catalog/stewardtools":              {imports: []string{"catalog", "catalog/governance", "platform/uuid", "steward/tool"}},
	"library/stewardtools":              {imports: []string{"library", "platform/uuid", "steward/tool"}},
	"operations/stewardtools":           {imports: []string{"operations", "platform/uuid", "steward/tool"}},
	"steward/web":                       {imports: []string{"identity", "platform/buildinfo", "platform/uuid", "platform/egress", "sources", "sources/musicbrainz", "sources/coverartarchive", "steward/runtime", "platform/secret", "steward/tool"}, http: true},
	"steward/provider":                  {imports: []string{"platform/egress"}, http: true},
	"steward/httpapi":                   {imports: []string{"steward/memory", "identity", "platform/httpserver", "platform/uuid", "platform/egress", "steward/provider", "steward/runtime", "platform/secret", "steward/tool", "steward/token", "steward/web"}, http: true},
	"steward/token":                     {imports: []string{"identity", "platform/uuid"}},
	"steward/mcp":                       {imports: []string{"platform/uuid", "identity", "platform/buildinfo", "platform/httpserver", "steward/token", "steward/tool"}, http: true},
	"steward/memory":                    {imports: []string{"platform/uuid", "steward/tool"}},
	"steward/runtime":                   {imports: []string{"steward/memory", "platform/uuid", "steward/provider", "steward/skills", "steward/tool"}},
	"steward/skills":                    {imports: []string{"identity", "steward/tool"}},
	"upstreammock":                      {imports: []string{"catalog/metadata", "catalog/textnorm", "insights/schema"}, http: true},
	"subsonic/protocol":                 {http: true},
	"subsonic":                          {imports: []string{"catalog", "subsonic/protocol"}},
	"subsonic/auth":                     {imports: []string{"identity", "platform/secret", "subsonic/protocol"}},
	"subsonic/httpapi":                  {imports: []string{"catalog", "identity", "listening", "personal", "playback", "playback/render", "zones", "platform/uuid", "platform/httpserver", "subsonic", "subsonic/auth", "subsonic/protocol"}, http: true},
	"platform/secret":                   {},
	"sources":                           {imports: []string{"platform/egress", "platform/buildinfo"}, http: true},
	"sources/coverartarchive":           {imports: []string{"sources", "sources/musicbrainz", "platform/uuid"}},
	"sources/acoustid":                  {imports: []string{"sources"}},
	"sources/wikidata":                  {imports: []string{"sources", "platform/uuid"}},
	"sources/commons":                   {imports: []string{"sources"}},
	"sources/wikipedia":                 {imports: []string{"sources"}},
	"sources/fanarttv":                  {imports: []string{"sources", "platform/uuid"}},
	"sources/theaudiodb":                {imports: []string{"sources", "platform/uuid"}},
	"sources/lrclib":                    {imports: []string{"sources"}},
	"sources/musicbrainz":               {imports: []string{"sources", "platform/uuid"}},
	"platform/egress":                   {http: true},
	"networkproxy":                      {imports: []string{"platform/egress", "platform/secret"}},
	"networkproxy/httpapi":              {imports: []string{"identity", "networkproxy", "platform/httpserver"}, http: true},
	"steward/tool":                      {imports: []string{"identity"}},
	"backup":                            {imports: []string{"platform/config", "platform/postgres"}},
	"backup/httpapi":                    {imports: []string{"backup", "identity", "platform/buildinfo", "platform/httpserver"}, http: true},
	"platform/buildinfo":                {},
	"platform/uuid":                     {},
	"platform/weborigin":                {},
	"platform/workpool":                 {},
	"platform/config":                   {imports: []string{"platform/weborigin", "platform/buildinfo"}},
	"platform/httpserver":               {http: true},
	"platform/postgres":                 {},
	"platform/postgres/postgrestest":    {imports: []string{"platform/uuid"}},
	"catalog/internal/releasesql":       {imports: []string{"catalog/internal/artistgraph", "catalog/textnorm"}},
	"catalog/classical":                 {},
	"catalog/metadata":                  {},
	"catalog/textnorm":                  {},
	"catalog/internal/artistgraph":      {imports: []string{"catalog/textnorm", "catalog/metadata", "catalog/internal/searchindex", "platform/uuid"}},
	"catalog/internal/writegate":        {},
	"catalog/internal/searchindex":      {imports: []string{"catalog/textnorm", "catalog/metadata", "catalog/internal/releasesql"}},
	"catalog/testsupport":               {imports: []string{"catalog/internal/artistgraph", "catalog/internal/searchindex", "catalog/textnorm"}},
	"catalog/maintenance":               {imports: []string{"catalog/internal/artistgraph", "catalog/textnorm", "catalog/internal/searchindex", "catalog/internal/writegate"}},
	"catalog/artwork":                   {},
	"catalog/ingest":                    {imports: []string{"catalog/classical", "catalog/textnorm", "catalog/internal/artistgraph", "catalog/metadata", "platform/uuid", "platform/workpool", "catalog/internal/searchindex", "catalog/internal/writegate"}},
	"catalog":                           {imports: []string{"catalog/internal/artistgraph", "catalog/internal/writegate", "catalog/internal/releasesql", "platform/uuid", "catalog/metadata", "catalog/textnorm", "catalog/internal/searchindex"}},
	"catalog/governance":                {imports: []string{"catalog/ingest", "personal/albumfavorites", "catalog/textnorm", "catalog/internal/artistgraph", "catalog", "catalog/internal/releasesql", "catalog/metadata", "operations", "platform/uuid", "catalog/internal/searchindex", "catalog/internal/writegate"}},
	"curation":                          {imports: []string{"sources", "operations", "identity", "catalog", "catalog/artwork", "catalog/metadata", "catalog/governance", "catalog/textnorm", "platform/uuid"}},
	"curation/model":                    {imports: []string{"curation", "curation/program", "catalog", "catalog/governance", "operations", "platform/uuid"}},
	"curation/stewardtools":             {imports: []string{"curation", "curation/program", "catalog", "catalog/governance", "operations", "platform/egress", "platform/uuid", "sources", "steward/tool"}, http: true},
	"curation/httpapi":                  {imports: []string{"curation", "identity", "operations", "platform/httpserver", "platform/uuid"}, http: true},
	"curation/program":                  {imports: []string{"curation", "catalog", "catalog/metadata", "catalog/textnorm", "operations", "sources", "platform/uuid"}},
	"operations":                        {imports: []string{"platform/uuid"}},
	"operations/httpapi":                {imports: []string{"identity", "operations", "platform/httpserver", "platform/uuid"}, http: true},
	"library/parser":                    {imports: []string{"catalog/metadata", "catalog/classical", "playback/dsdio"}},
	"library/parser/parsertest":         {},
	"library":                           {imports: []string{"catalog", "catalog/governance", "catalog/artwork", "catalog/ingest", "catalog/metadata", "library/parser", "platform/uuid", "platform/workpool"}},
	"dspsettings/irload":                {imports: []string{"dsp/src", "dsp/convolution", "playback/render/ffmpeg"}},
	"dsp/convolution":                   {imports: []string{"dsp/internal/fft"}},
	"dsp/internal/fft":                  {},
	"dsp/internal/refdata":              {},
	"dsp/src":                           {imports: []string{"dsp/internal/fft"}},
	"dsp/chain":                         {imports: []string{"dsp/leveling", "dsp/fade", "dsp/dither"}},
	"dsp/leveling":                      {},
	"dsp/fade":                          {},
	"dsp/dither":                        {},
	"dsp/downmix":                       {},
	"dsp/sdm":                           {},
	"dsp/sdm/internal/gen":              {},
	"dsp/dsd":                           {},
	"dsp/dsd/internal/gen":              {},
	"playback/dsdio":                    {},
	"playback/dsdpcm":                   {imports: []string{"playback/dsdio", "dsp/dsd", "dsp/src", "dsp/peq"}},
	"dsp/peq":                           {},
	"dspsettings":                       {imports: []string{"zones", "dsp/peq", "dsp/convolution", "dspsettings/opra", "dspsettings/irload", "platform/uuid"}},
	"dspsettings/httpapi":               {imports: []string{"dspsettings", "dspsettings/irload", "zones", "identity", "platform/httpserver", "platform/uuid"}, http: true},
	"dspsettings/opra":                  {},
	"playback/analysis":                 {},
	"playback/render/ffmpeg":            {},
	"playback/render":                   {imports: []string{"zones", "dsp/chain", "dsp/leveling", "dsp/fade", "dsp/dither", "dsp/src", "dsp/downmix", "dsp/peq", "dsp/convolution", "playback/dsdio", "playback/dsdpcm", "playback/render/ffmpeg", "platform/uuid"}},
	"playback/fingerprint":              {},
	"playback":                          {imports: []string{"catalog", "library", "playback/analysis", "playback/dsdio", "playback/dsdpcm", "playback/fingerprint"}},
	"ptp":                               {imports: []string{"devices/lan", "devices/mdns"}, http: true},
	"devices/airplay":                   {imports: []string{"devices/airplay/bplist", "devices/airplay/hap", "devices/airplay/raop", "devices/airplay/rtp", "devices/airplay/rtsp", "devices/lan", "devices/mdns", "platform/uuid", "ptp", "platform/secret"}, http: true},
	"devices/airplay/rtp":               {imports: []string{"devices/lan"}},
	"devices/airplay/raop":              {imports: []string{"devices/airplay/rtp", "devices/airplay/rtsp"}, http: true},
	"devices/airplay/bplist":            {},
	"devices/airplay/hap":               {imports: []string{"devices/airplay/rtsp", "platform/uuid"}, http: true},
	"devices/airplay/rtsp":              {imports: []string{"devices/lan"}, http: true},
	"devices/lan":                       {http: true},
	"devices/mdns":                      {imports: []string{"devices/lan"}},
	"devices/cast/castv2":               {imports: []string{"devices/lan"}},
	"devices/cast":                      {imports: []string{"devices/cast/castv2", "devices/lan", "devices/mdns", "zones"}},
	"devices/slimproto":                 {imports: []string{"devices/lan", "zones"}},
	"devices/slimproto/slimtest":        {http: true},
	"devices/fakecast":                  {imports: []string{"devices/cast/castv2", "devices/lan", "devices/mdns", "platform/uuid"}, http: true},
	"devices/upnp":                      {imports: []string{"devices/airplay", "devices/airplay/hap", "devices/airplay/rtsp", "devices/cast", "devices/lan", "devices/slimproto", "identity", "platform/uuid", "ptp", "zones"}, http: true},
	"devices/upnp/httpapi":              {imports: []string{"devices/airplay", "devices/cast", "devices/upnp", "identity", "platform/httpserver"}, http: true},
	"devices/fakerenderer":              {imports: []string{"devices/lan", "platform/uuid"}, http: true},
	"zones/queue":                       {},
	"zones/httpapi":                     {imports: []string{"identity", "platform/httpserver", "platform/uuid", "zones"}, http: true},
	"zones/zonestest":                   {imports: []string{"zones"}},
	"zones":                             {imports: []string{"catalog", "platform/uuid", "zones/queue"}},
	"listening":                         {imports: []string{"catalog"}},
	"personal/albumfavorites":           {},
	"personal":                          {imports: []string{"catalog", "platform/uuid", "personal/albumfavorites"}},
	"personal/httpapi":                  {imports: []string{"identity", "personal", "platform/httpserver", "platform/uuid"}, http: true},
	"identity":                          {imports: []string{"platform/httpserver", "platform/weborigin"}, http: true},
	"identity/httpapi":                  {imports: []string{"identity", "platform/httpserver", "platform/uuid"}, http: true},
	"catalog/httpapi":                   {imports: []string{"catalog", "catalog/artwork", "catalog/governance", "identity", "operations", "platform/httpserver", "platform/uuid"}, http: true},
	"library/httpapi":                   {imports: []string{"identity", "library", "platform/httpserver", "platform/uuid"}, http: true},
	"playback/httpapi":                  {imports: []string{"identity", "platform/httpserver", "platform/uuid", "playback"}, http: true},
	"listening/httpapi":                 {imports: []string{"identity", "listening", "platform/httpserver", "platform/uuid"}, http: true},
	"app": {imports: []string{"networkproxy", "networkproxy/httpapi", "platform/egress", "subsonic/auth", "subsonic/httpapi", "insights", "insights/httpapi", "platform/buildinfo", "plugins", "plugins/httpapi",
		"curation", "curation/program", "curation/httpapi", "curation/model", "curation/stewardtools", "steward/provider", "sources", "sources/acoustid", "playback/fingerprint", "sources/musicbrainz", "sources/coverartarchive", "sources/lrclib",
		"sources/wikidata", "sources/commons", "sources/wikipedia", "sources/fanarttv", "sources/theaudiodb",
		"platform/diagnostics", "platform/diagnostics/httpapi", "platform/diagnostics/stewardtools", "backup/stewardtools", "identity/stewardtools", "playback/stewardtools", "dspsettings/stewardtools",
		"devices/airplay", "devices/upnp", "devices/upnp/httpapi", "zones/stewardtools", "listening/stewardtools", "personal/stewardtools",
		"catalog/stewardtools", "catalog/governance/stewardtools", "library/stewardtools", "operations", "operations/stewardtools",
		"steward/httpapi", "steward/runtime", "platform/secret", "steward/tool",
		"steward/token", "steward/mcp", "steward/web", "steward/memory", "steward/skills",
		"dspsettings", "dspsettings/httpapi", "dspsettings/irload", "dsp/chain", "backup", "backup/httpapi", "zones", "zones/queue", "zones/httpapi",
		"catalog", "catalog/artwork", "catalog/governance", "catalog/httpapi", "catalog/maintenance", "identity", "identity/httpapi", "library", "library/httpapi", "listening", "listening/httpapi",
		"personal", "personal/httpapi", "operations/httpapi", "platform/config", "platform/httpserver", "playback", "playback/analysis", "playback/httpapi", "playback/render", "playback/render/ffmpeg",
	}, http: true},
}

func TestPackageDependencyDirections(t *testing.T) {
	internalDirectory := filepath.Join("..")
	found := map[string]bool{}
	err := filepath.WalkDir(internalDirectory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || !entry.IsDir() {
			return walkErr
		}
		if entry.Name() == "testdata" {
			return filepath.SkipDir
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
			// DSP 也不能绕过 internal 规则导入迁移等项目包。
			if strings.HasPrefix(relative, "dsp/") && strings.HasPrefix(imported, backendPrefix) && !strings.HasPrefix(imported, internalPrefix) {
				t.Errorf("internal/%s must not import %s", relative, imported)
			}
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
