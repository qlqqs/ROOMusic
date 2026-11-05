package app

import (
	"reflect"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func recordAdjudicationMergedSources(f *artistSamples) {
	base := "/api/v1/artists/" + f.primary
	path := base + "/merged-sources"
	get := func(name string) map[string]any {
		return adjudicationRecord(f, name, f.call("GET", path, nil, &f.admin), 200)
	}
	artistSampleItems(f.t, get("merged-sources-empty"), 0)
	merge := func() map[string]any {
		return adjudicationSuccess(f, "POST", base+"/merge", map[string]any{
			"expected_revision": f.revision(f.primary), "source_artist_id": f.alias, "source_expected_revision": f.revision(f.alias),
		})
	}
	merged := merge()
	candidate := artistSampleItems(f.t, get("merged-sources"), 1)[0].(map[string]any)
	if candidate["artist_id"] != f.alias || candidate["name"] != "Teresa Teng" || candidate["revision"] != f.revision(f.alias) || candidate["operation_id"] != merged["operation_id"] || !reflect.DeepEqual(candidate["name_key_ids"], []any{f.key(f.alias)}) {
		f.t.Fatalf("合并来源没有真实恢复身份/修订/操作/名字键：%v", candidate)
	}
	if at, ok := candidate["merged_at"].(string); !ok {
		f.t.Fatal("合并来源缺少时间")
	} else if _, err := time.Parse(time.RFC3339Nano, at); err != nil {
		f.t.Fatalf("合并来源时间不是 RFC3339：%q", at)
	}
	// 不改队列身份；重新读取的曲目元数据必须立即采用合并后的艺人身份。
	summary := adjudicationRecord(f, "tracks-summaries-after-merge", f.call("POST", "/api/v1/tracks/summaries", map[string]any{"track_ids": []string{f.releases[1].trackIDs[0]}}, &f.member), 200)
	track := artistSampleItems(f.t, summary, 1)[0].(map[string]any)
	refs := track["artist_refs"].([]any)
	if len(refs) != 1 || refs[0].(map[string]any)["artist_id"] != f.primary || refs[0].(map[string]any)["name"] != "Teresa Teng" {
		f.t.Fatalf("治理成功后摘要没有刷新引用或丢失署名原文：%v", track)
	}
	split := func(keys any, target map[string]any) map[string]any {
		scope := map[string]any{"mode": "aliases", "name_key_ids": keys}
		preview := adjudicationSuccess(f, "POST", base+"/split-preview", scope)
		return adjudicationSuccess(f, "POST", base+"/split", map[string]any{
			"expected_revision": preview["revision"], "mode": "aliases", "name_key_ids": keys, "basis": preview["basis"], "target": target,
		})
	}
	// 直接使用只读候选返回的三个恢复输入，证明它确实能支持恢复，而非仅展示操作历史。
	restored := split(candidate["name_key_ids"], map[string]any{"kind": "restore", "artist_id": candidate["artist_id"], "expected_revision": candidate["revision"]})
	if restored["target"].(map[string]any)["id"] != f.alias {
		f.t.Fatal("恢复候选未恢复旧身份")
	}
	artistSampleItems(f.t, get("merged-sources-after-restore"), 0)
	merge()
	created := split([]string{f.key(f.alias)}, map[string]any{"kind": "new", "display_name": "独立的新艺人"})
	if id, ok := created["target"].(map[string]any)["id"].(string); !ok || !uuid.Valid(id) || id == f.alias {
		f.t.Fatal("移走合并名字键未产生独立身份")
	}
	empty := artistSampleItems(f.t, get("merged-sources-empty-keys"), 1)[0].(map[string]any)
	if empty["artist_id"] != f.alias || !reflect.DeepEqual(empty["name_key_ids"], []any{}) {
		f.t.Fatalf("当前不再拥有的键未剔除或空键输出不是数组：%v", empty)
	}
	adjudicationError(f, "merged-sources-invalid-id", f.call("GET", "/api/v1/artists/bad/merged-sources", nil, &f.admin), 400, "invalid_id")
	adjudicationError(f, "merged-sources-not-found", f.call("GET", "/api/v1/artists/"+adjudicationUnknownTrack+"/merged-sources", nil, &f.admin), 404, "not_found")
}
