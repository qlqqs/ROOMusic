// Package stewardtools 将目录的公开读取能力适配为管家只读工具。
package stewardtools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

const pageProperties = `"page":{"type":"integer","minimum":1,"maximum":1000000},"page_size":{"type":"integer","minimum":1,"maximum":50}`
const idProperty = `"id":{"type":"string","minLength":36,"maxLength":36}`
const queryProperty = `"q":{"type":"string","maxLength":200}`
const trackRoles = `"track_artist","lyricist","composer","arranger","conductor","performer","producer"`
const pageDescription = "page 从 1 开始，默认 1；page_size 默认 20、最大 50；根据 pagination 翻页。"

type input struct {
	ID            string `json:"id"`
	Query         string `json:"q"`
	Type          string `json:"type"`
	Page          int    `json:"page"`
	PageSize      int    `json:"page_size"`
	Attention     string `json:"attention"`
	Sort          string `json:"sort"`
	Role          string `json:"role"`
	Initial       string `json:"initial"`
	Participation string `json:"participation"`
	Status        string `json:"status"`
	Category      string `json:"category"`
}

func definition(name, description string, role tool.Role, properties, required string, read func(context.Context, input) (any, error)) tool.Definition {
	return tool.Definition{
		Name: name, Description: description,
		Input: json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{` + properties + `},"required":[` + required + `]}`),
		// DTO 含 nullable 字段，当前 schema 子集不支持联合类型；输出复用业务 DTO 的对象合同。
		Output: json.RawMessage(`{"type":"object"}`), MinRole: role, Scope: tool.ScopeRead, Untrusted: true,
		Handler: func(ctx context.Context, _ tool.Caller, raw json.RawMessage) (json.RawMessage, error) {
			p := input{Page: 1, PageSize: 20}
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, err
			}
			if p.ID != "" && !uuid.Valid(p.ID) {
				return nil, &tool.Error{Code: tool.CodeInvalidInput, Message: "标识必须是 UUID"}
			}
			p.ID = strings.ToLower(p.ID)
			if strings.IndexFunc(p.Query, func(r rune) bool { return unicode.IsControl(r) && !unicode.IsSpace(r) }) >= 0 {
				return nil, &tool.Error{Code: tool.CodeInvalidInput, Message: "搜索词包含控制字符"}
			}
			p.Query = strings.TrimSpace(p.Query)
			if len(p.Query) > 200 {
				return nil, &tool.Error{Code: tool.CodeInvalidInput, Message: "搜索词最多 200 字节"}
			}
			value, err := read(ctx, p)
			if errors.Is(err, catalog.ErrReleaseNotFound) || errors.Is(err, catalog.ErrReleaseGroupNotFound) || errors.Is(err, catalog.ErrTrackNotFound) || errors.Is(err, catalog.ErrArtistNotFound) {
				return nil, &tool.Error{Code: tool.CodeTargetNotFound, Message: "目标不存在"}
			}
			if err != nil {
				return nil, err
			}
			return json.Marshal(value)
		},
	}
}

func pagination(p input, total int64) map[string]any {
	return map[string]any{"page": p.Page, "page_size": p.PageSize, "total": total}
}

// StewardTools 提供目录与元数据问题的只读工具；调用权限由工具注册表逐次检查。
func StewardTools(store *catalog.Store, issues *governance.Store) []tool.Definition {
	return []tool.Definition{
		definition("search_library", "按关键词搜索曲库时使用。q 是搜索词，type 为 albums、tracks 或 artists；返回对应摘要、命中依据与 pagination，total_relation=gte 表示总数是下界，应按 has_more 翻页。"+pageDescription,
			tool.RoleMember, queryProperty+`,"type":{"type":"string","enum":["albums","tracks","artists"]},`+pageProperties, `"q","type"`,
			func(ctx context.Context, p input) (any, error) {
				return store.Search(ctx, catalog.SearchParams{Query: p.Query, Type: p.Type, Page: p.Page, PageSize: p.PageSize})
			}),
		definition("list_albums", "浏览专辑或查找待关注专辑时使用。返回专辑组摘要和代表版本；q 可筛选关键词，attention=required 只列待关注项，按标题排序。"+pageDescription,
			tool.RoleMember, queryProperty+`,"attention":{"type":"string","enum":["required"]},`+pageProperties, "",
			func(ctx context.Context, p input) (any, error) {
				result, err := store.ListReleaseGroups(ctx, catalog.ListParams{Search: p.Query, AttentionRequired: p.Attention == "required", Page: p.Page, PageSize: p.PageSize})
				return map[string]any{"items": result.Items, "pagination": pagination(p, result.Total)}, err
			}),
		definition("get_album", "查看一张专辑的详情及版本时使用，id 是专辑组 UUID。返回专辑摘要、默认版本与 releases；pagination 只对 releases 分页，服务层最多提供前 100 个版本，releases_truncated 标明是否超过此上限。"+pageDescription,
			tool.RoleMember, idProperty+","+pageProperties, `"id"`, albumReader(store)),
		definition("get_release", "查看发行版本及曲目时使用，id 是版本 UUID。返回版本详情、署名、证据摘要及 media；pagination 对所有介质的曲目统一分页，空介质不返回。不含歌词正文。"+pageDescription,
			tool.RoleMember, idProperty+","+pageProperties, `"id"`, releaseReader(store)),
		definition("get_track", "查看单首曲目时使用，id 是曲目 UUID。返回安全曲目摘要及 detail（曲序、音频事实、作品、署名和文件名）；缺失曲目的 detail 为 null。不读取歌词，不分页。",
			tool.RoleMember, idProperty, `"id"`, trackReader(store)),
		definition("list_artists", "浏览艺人时使用。返回艺人摘要与参与专辑、曲目计数；可按 q、role、initial 筛选，initial 为 A–Z、kana 或 #；sort 默认为 name，也可为 album_count。"+pageDescription,
			tool.RoleMember, artistListProperties(), "",
			func(ctx context.Context, p input) (any, error) {
				if p.Sort == "" {
					p.Sort = "name"
				}
				return store.ListArtists(ctx, catalog.ArtistListParams{Query: p.Query, Role: p.Role, Initial: p.Initial, Sort: p.Sort, Page: p.Page, PageSize: p.PageSize})
			}),
		definition("get_artist", "查看艺人身份、名字与各角色参与数量时使用，id 是艺人 UUID。返回 artist、revision、name_source、names、names_pagination、role_counts 和 redirect_artist_id；名字固定返回服务层首批 20 条，本工具不分页。",
			tool.RoleMember, idProperty, `"id"`,
			func(ctx context.Context, p input) (any, error) { return store.LoadArtist(ctx, p.ID) }),
		definition("list_artist_albums", "查看艺人的专辑时使用，id 是艺人 UUID。默认只列专辑艺人关系，participation=any 列所有角色参与的专辑，q 筛选专辑标题；返回专辑组摘要及 credited_release_id。"+pageDescription,
			tool.RoleMember, idProperty+","+queryProperty+`,"participation":{"type":"string","enum":["any"]},`+pageProperties, `"id"`,
			func(ctx context.Context, p input) (any, error) {
				return store.ListArtistAlbums(ctx, p.ID, p.Page, p.PageSize, catalog.ArtistAlbumOptions{Participation: p.Participation, Query: p.Query})
			}),
		definition("list_artist_tracks", "按署名角色查看艺人的曲目时使用。id 为艺人 UUID；role 必填，可为 track_artist、lyricist、composer、arranger、conductor、performer、producer。返回安全曲目摘要，包含 present=false 的缺失曲目。"+pageDescription,
			tool.RoleMember, idProperty+`,"role":{"type":"string","enum":[`+trackRoles+`]},`+pageProperties, `"id","role"`,
			func(ctx context.Context, p input) (any, error) {
				return store.ListArtistTracks(ctx, p.ID, p.Role, p.Page, p.PageSize)
			}),
		definition("get_release_evidence", "管理员追溯版本标签值与整理来源时使用，id 为版本 UUID。返回字段选中值、扫描候选值、来源类别、置信度、规则与归组相对来源路径。沿用接口的有界证据与 truncated 标记，不分页；不返回歌词或原始文件。",
			tool.RoleAdmin, idProperty, `"id"`, evidenceReader(store)),
		definition("list_release_issues", "管理员查找待关注版本时使用。status 默认为 open，也可为 reviewed；category 可为 fields、grouping、missing_tracks，省略时列全部类别。返回版本摘要、问题计数、summary 与 pagination。"+pageDescription,
			tool.RoleAdmin, `"status":{"type":"string","enum":["open","reviewed"]},"category":{"type":"string","enum":["fields","grouping","missing_tracks"]},`+pageProperties, "",
			func(ctx context.Context, p input) (any, error) {
				if p.Status == "" {
					p.Status = governance.IssueStatusOpen
				}
				result, err := issues.ListIssues(ctx, governance.IssueListParams{Status: p.Status, Category: p.Category, Page: p.Page, PageSize: p.PageSize})
				return map[string]any{"items": result.Items, "summary": result.Summary, "pagination": pagination(p, result.Total)}, err
			}),
		definition("get_release_issues", "管理员查看版本的字段、归组和缺失曲目问题时使用，id 为版本 UUID。返回问题及核对状态，不返回核对依据令牌或核对人；missing_tracks.pagination 对缺失曲目明细分页，原有 truncated 表示服务层明细上限。不对字段与归组分页。"+pageDescription,
			tool.RoleAdmin, idProperty+","+pageProperties, `"id"`, issueReader(issues)),
	}
}

func artistListProperties() string {
	initials := `"kana","#"`
	for c := 'A'; c <= 'Z'; c++ {
		initials += `,"` + string(c) + `"`
	}
	return queryProperty + `,"role":{"type":"string","enum":["release_artist","album_artist",` + trackRoles + `]},"initial":{"type":"string","enum":[` + initials + `]},"sort":{"type":"string","enum":["name","album_count"]},` + pageProperties
}
