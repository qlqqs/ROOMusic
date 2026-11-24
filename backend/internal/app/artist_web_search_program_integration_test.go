package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPostgreSQLArtistWebSearchProgramScope(t *testing.T) {
	f := newArtistWebSearchFixture(t, "", true, false, nil)
	app := f.application
	f.configureMock(t)
	for _, name := range []string{"缺资料主艺人", "已有 MB 对应", "已有 WD 对应", "人工资料齐全", "并入既有对应"} {
		f.addRelease(t, name)
	}
	track := f.addRelease(t, "有合作者的主艺人")
	mustExec(t, app, `INSERT INTO track_credits(track_id,role,name,position) VALUES($1,'composer','只作曲的合作者',1)`, track)
	trackOnly := f.addRelease(t, "艺人专辑署名")
	mustExec(t, app, `UPDATE tracks SET artist='只有曲目主署名' WHERE id=$1`, trackOnly)
	publishSearch(t, app)

	main := f.artistID(t, "缺资料主艺人")
	mb := f.artistID(t, "已有 MB 对应")
	wd := f.artistID(t, "已有 WD 对应")
	full := f.artistID(t, "人工资料齐全")
	merged := f.artistID(t, "并入既有对应")
	collaborator := f.artistID(t, "只作曲的合作者")
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM artist_effective_credits WHERE artist_id=$1 AND role='composer'`, collaborator)
	linkArtistMedia(t, app, mb, artistMediaMBID)
	mustExec(t, app, `INSERT INTO external_links(id,source,object_type,object_id,external_id,method,confidence)
		VALUES(gen_random_uuid(),'wikidata','artist',$1,'Q900005','artist_credit','certain')`, wd)
	merge := &artistMergeFixture{programFixture: &programFixture{curationFixture: f.curationFixture}}
	stale := f.pendingArtist(t, merged, "并入既有对应")
	conversation := modelConversation(t, f.curationFixture)
	modelClaim(t, f.curationFixture, conversation, stale)
	makeException := func(externalID string) string {
		t.Helper()
		input, err := json.Marshal(webSearchIdentityProposal(stale.ID, "wikidata", externalID, f.endpoint+"/searxng/artists/coast"))
		requireCurationOK(t, err)
		id, err := f.writer.CreateException(t.Context(), curation.ModelException{
			PendingItemID: stale.ID, PlanItemID: uuid.New(), ConversationID: &conversation,
			Tool: "resolve_artist_identity", Provider: "mock-model", Input: input,
			Preview: json.RawMessage(`{"changes":[{"field":"artist_link","after":{"name":"并入既有对应","description":null}}]}`),
			Review:  curation.ExceptionReview{ID: uuid.New(), Model: "mock-reviewer", Decision: "needs_human_confirmation", Source: "model"},
		})
		requireCurationOK(t, err)
		return id
	}
	open := makeException("Q900001")
	accepting := makeException("Q900004")
	requireCurationOK(t, f.writer.ClaimException(t.Context(), accepting, f.admin.id))
	merge.manualMerge(t, mb, merged)

	revision := readArtistMedia(t, app, &f.admin, full).Revision
	requireArtistMediaEdit(t, artistImageUpload(t, app, &f.admin, full, uuid.New(), revision, externalFixturePNG(t, 3, 4)), full, "", revision+1)
	requireArtistMediaEdit(t, artistMediaCommand(t, app, &f.admin, http.MethodPut, full, "/bio", uuid.New(),
		map[string]any{"expected_revision": revision + 1, "text": "这段简介由管理员手动编写。", "lang": "zh"}), full, "", revision+2)

	// 原先排队后并入的旧项必须收尾，家族已有对应也不能再为旧身份排队。
	f.enable(t)
	f.clock.advance(t, time.Minute)
	for _, artist := range []string{mb, wd, full, merged, collaborator} {
		b3RequireCount(t, f.application, 0, `SELECT count(*) FROM curation_pending_items WHERE artist_id=$1 AND status<>'resolved'`, artist)
	}
	f.waitArtistResolution(t, stale.ID, "artist_out_of_scope")
	b3RequireCount(t, app, 1, `SELECT count(*) FROM curation_model_exceptions WHERE id=$1 AND status='superseded' AND decided_at IS NOT NULL`, open)
	b3RequireCount(t, app, 1, `SELECT count(*) FROM curation_model_exceptions WHERE id=$1 AND status='accepting' AND decided_at IS NULL AND decided_by=$2`, accepting, f.admin.id)
	for _, name := range []string{"缺资料主艺人", "有合作者的主艺人", "只有曲目主署名", "艺人专辑署名"} {
		f.status(t, f.artistID(t, name), "queued", 0)
	}

	item := f.artistItem(t, main)
	evidence := decodeArtistWorkEvidence(t, item)
	if !slices.Contains(evidence.Names, "缺资料主艺人") || len(evidence.Albums) != 1 ||
		evidence.Albums[0].Name != "沿岸合成专辑" || evidence.Albums[0].Year != 2024 ||
		!slices.Equal(evidence.Tracks, []string{"潮声合成曲目"}) || !evidence.Gaps.Image || !evidence.Gaps.Bio {
		t.Fatalf("主署名待定项证据 %+v", evidence)
	}
	f.clock.advance(t, time.Minute)
	again := f.artistItem(t, main)
	if again.ID != item.ID || again.InputDigest != item.InputDigest || !again.UpdatedAt.Equal(item.UpdatedAt) {
		t.Errorf("输入未变时待定项被重建或改写，首轮 %+v，次轮 %+v", item, again)
	}
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM curation_pending_items WHERE artist_id=$1`, main)

	const insertPending = `INSERT INTO curation_pending_items(id,target_type,artist_id,kind,status,rule_version,input_digest,evidence,resolved_at,resolution)
		VALUES(gen_random_uuid(),'artist',$1,'artist_identity',$2,1,'直接写入索引夹具','{}',
		CASE WHEN $2='resolved' THEN now() END,CASE WHEN $2='resolved' THEN 'filled' END)`
	_, err := app.database.ExecContext(t.Context(), insertPending, main, "waiting_model")
	var failure interface{ SQLState() string }
	if !errors.As(err, &failure) || failure.SQLState() != "23505" {
		t.Fatalf("第二条未结束项应被唯一索引拒绝，实际 %v", err)
	}
	mustExec(t, app, insertPending, main, "resolved")
	b3RequireCount(t, f.application, 2, `SELECT count(*) FROM curation_pending_items WHERE artist_id=$1`, main)
}

func TestPostgreSQLArtistWebSearchProgramPluginName(t *testing.T) {
	manifest, wasm := demoPluginFiles(t)
	for _, tc := range []struct {
		name                    string
		webEnabled, manualImage bool
	}{
		{"留下图片缺口", true, false},
		{"插件补齐最后一项", true, true},
		{"网络搜索关闭时仍查插件", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const name = "折纸灯乐团"
			var named atomic.Bool
			f := newArtistWebSearchFixture(t, "", true, false, func(mock http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					q := r.URL.Query()
					if r.URL.Path == "/plugins/lyrics-demo/main/artist_bio" && q.Get("name") == name && q.Get("mbid") == "" {
						named.Store(true)
					}
					mock.ServeHTTP(w, r)
				})
			})
			app := f.application
			f.configureMock(t)
			artist := f.addArtist(t, name)
			if tc.manualImage {
				revision := readArtistMedia(t, app, &f.admin, artist).Revision
				requireArtistMediaEdit(t, artistImageUpload(t, app, &f.admin, artist, uuid.New(), revision, externalFixturePNG(t, 3, 4)), artist, "", revision+1)
			}
			_, preview := previewDemoPlugin(t, app, f.admin, demoPluginArchive(t, manifest, wasm))
			installed := app.request(t, http.MethodPost, pluginAPIBase, map[string]string{"preview_token": preview.Token}, &f.admin, nil)
			if installed.Code != http.StatusCreated {
				t.Fatalf("安装示例插件 HTTP %d %s", installed.Code, installed.Body)
			}
			f.update(t, http.MethodPut, curationBase+"/sources/"+demoPluginSource, map[string]bool{"enabled": true})
			f.update(t, http.MethodPut, curationBase+"/sources/web_search", map[string]bool{"enabled": tc.webEnabled})
			f.enable(t)
			f.clock.advance(t, time.Minute)

			detail := readArtistMedia(t, app, &f.admin, artist)
			if detail.Bio == nil || detail.Bio.Source != demoPluginSource || detail.Bio.Text == "" {
				t.Fatalf("插件没有按名字写简介 %+v", detail.Bio)
			}
			b3RequireCount(t, f.application, 0, `SELECT count(*) FROM external_links WHERE object_type='artist' AND object_id=$1`, artist)
			if !named.Load() {
				t.Fatal("没有按艺人名且无 MBID 的插件请求")
			}
			if !tc.webEnabled || tc.manualImage {
				f.status(t, artist, "none", 0)
				b3RequireCount(t, f.application, 0, `SELECT count(*) FROM curation_pending_items WHERE artist_id=$1`, artist)
				return
			}

			f.status(t, artist, "queued", 0)
			item := f.artistItem(t, artist)
			evidence := decodeArtistWorkEvidence(t, item)
			if !evidence.Gaps.Image || evidence.Gaps.Bio {
				t.Fatalf("插件写入后留下的待定项证据 %+v", evidence)
			}
		})
	}
}
