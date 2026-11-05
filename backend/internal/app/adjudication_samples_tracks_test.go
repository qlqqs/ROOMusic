package app

import (
	"fmt"
	"strings"
)

const adjudicationUnknownTrack = "e0000000-0000-4000-8000-999999999999"

func recordAdjudicationTrackSummaries(f *artistSamples) {
	path := "/api/v1/tracks/summaries"
	missing, alias, present, unavailable := f.releases[0].trackIDs[2], f.releases[1].trackIDs[0], f.releases[0].trackIDs[0], f.releases[4].trackIDs[0]
	ids := []string{missing, adjudicationUnknownTrack, alias, present, strings.ToUpper(missing), unavailable}
	response := f.call("POST", path, map[string]any{"track_ids": ids}, &f.member)
	body := adjudicationRecord(f, "tracks-summaries", response, 200)
	items := artistSampleItems(f.t, body, 4)
	for i, id := range []string{missing, alias, present, unavailable} {
		item := items[i].(map[string]any)
		wantArtist := f.primary
		if id == alias {
			wantArtist = f.alias
		} else if id == unavailable {
			wantArtist = f.missing
		}
		if item["track_id"] != id || item["present"] != (id == alias || id == present) {
			f.t.Fatalf("批量摘要未保持首次输入顺序、去重或缺失状态：%v", body)
		}
		refs, ok := item["artist_refs"].([]any)
		if !ok || len(refs) != 1 || refs[0].(map[string]any)["artist_id"] != wantArtist {
			f.t.Fatalf("批量摘要没有当前位置艺人引用：%v", item)
		}
	}
	for _, key := range []string{"relative_path", "source_root_id", "content_hash", "password", "source_path"} {
		if strings.Contains(response.Body.String(), `"`+key+`"`) {
			f.t.Fatalf("批量摘要泄露非安全字段 %s", key)
		}
	}
	unknown := adjudicationRecord(f, "tracks-summaries-unknown", f.call("POST", path, map[string]any{"track_ids": []string{adjudicationUnknownTrack}}, &f.member), 200)
	artistSampleItems(f.t, unknown, 0)
	oversized := make([]string, 1001)
	for i := range oversized {
		oversized[i] = fmt.Sprintf("e1000000-0000-4000-8000-%012d", i+1)
	}
	for _, tc := range []struct {
		name string
		ids  []string
	}{
		{"empty", []string{}}, {"invalid-id", []string{present, "not-a-uuid"}}, {"over-limit", oversized},
	} {
		adjudicationError(f, "tracks-summaries-"+tc.name, f.call("POST", path, map[string]any{"track_ids": tc.ids}, &f.member), 400, "invalid_input")
	}
	valid := map[string]any{"track_ids": []string{present}}
	adjudicationError(f, "tracks-summaries-unauthorized", f.call("POST", path, valid, nil), 401, "unauthorized")
	for _, tc := range []struct{ name, origin, code string }{
		{"origin-forbidden", "https://foreign.example", "origin_forbidden"}, {"origin-required", "", "origin_required"},
	} {
		adjudicationError(f, "tracks-summaries-"+tc.name, f.call("POST", path, valid, &f.member, map[string]string{"Origin": tc.origin}), 403, tc.code)
	}
}
