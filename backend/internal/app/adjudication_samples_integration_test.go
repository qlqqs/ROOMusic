package app

import (
	"net/http/httptest"
	"strings"
	"testing"
)

const adjudicationSampleScope = "artists/adjudication"

// 只管理裁决增量；真实应用负责路由、认证、同源检查和响应，复用既有脱敏器而不手写 golden。
// 更新：ROOMUSIC_UPDATE_API_SAMPLES=1 go test ./internal/app -run '^TestPostgreSQLArtistAdjudicationResponseSamples$' -count=1。
func TestPostgreSQLArtistAdjudicationResponseSamples(t *testing.T) {
	f := newArtistSamples(t)
	f.samples.scope = adjudicationSampleScope
	prepareAdjudicationAlbums(f)
	recordAdjudicationPreparing(f)
	f.publish()
	recordAdjudicationAlbums(f)
	recordAdjudicationRoles(f)
	recordAdjudicationTrackSummaries(f)
	recordAdjudicationMergedSources(f)
	for _, name := range strings.Fields(adjudicationSampleNames) {
		if _, ok := f.samples.samples[adjudicationSampleScope+"/"+name]; !ok {
			t.Fatalf("未生成裁决样本：%s", name)
		}
	}
	if len(f.samples.samples) != len(strings.Fields(adjudicationSampleNames)) {
		t.Fatalf("裁决样本清单与实际产出数量不一致：%d", len(f.samples.samples))
	}
	f.samples.finish()
}

func adjudicationRecord(f *artistSamples, name string, response *httptest.ResponseRecorder, status int) map[string]any {
	f.t.Helper()
	f.samples.record(adjudicationSampleScope+"/"+name, response, status)
	return decodeJSONMap(f.t, response)
}

func adjudicationGet(f *artistSamples, name, path string) map[string]any {
	f.t.Helper()
	return adjudicationRecord(f, name, f.call("GET", path, nil, &f.member), 200)
}

func adjudicationError(f *artistSamples, name string, response *httptest.ResponseRecorder, status int, code string) {
	f.t.Helper()
	requireAPIError(f.t, response, status, code)
	adjudicationRecord(f, name, response, status)
}

func adjudicationSuccess(f *artistSamples, method, path string, body any) map[string]any {
	f.t.Helper()
	r := f.call(method, path, body, &f.admin)
	if r.Code != 200 {
		f.t.Fatalf("裁决夹具操作 %s %s：%d %s", method, path, r.Code, r.Body.String())
	}
	return decodeJSONMap(f.t, r)
}

func recordAdjudicationPreparing(f *artistSamples) {
	path := "/api/v1/artists/" + f.primary + "/merged-sources"
	adjudicationError(f, "merged-sources-preparing", f.call("GET", path, nil, &f.admin), 503, "artist_catalog_preparing")
	adjudicationError(f, "merged-sources-unauthorized", f.call("GET", path, nil, nil), 401, "unauthorized")
	adjudicationError(f, "merged-sources-forbidden", f.call("GET", path, nil, &f.member), 403, "forbidden")
	adjudicationError(f, "albums-any-preparing", f.call("GET", "/api/v1/artists/"+f.primary+"/albums?participation=any", nil, &f.member), 503, "artist_catalog_preparing")
	body := adjudicationRecord(f, "tracks-summaries-preparing", f.call("POST", "/api/v1/tracks/summaries", map[string]any{"track_ids": []string{f.releases[0].trackIDs[0]}}, &f.member), 200)
	item := artistSampleItems(f.t, body, 1)[0].(map[string]any)
	for _, value := range []map[string]any{item, item["release"].(map[string]any)} {
		if refs, ok := value["artist_refs"].([]any); !ok || len(refs) != 0 {
			f.t.Fatalf("整理期摘要仍须可读且引用为空数组：%v", body)
		}
	}
}

func TestArtistAdjudicationSampleOwnership(t *testing.T) {
	name := adjudicationSampleScope + "/merged-sources.json"
	for _, scope := range []string{"", "artists", "artist-increment", "scan-feedback", adjudicationSampleScope} {
		if got := (&apiSampleRecorder{scope: scope}).ownsSample(name); got != (scope == adjudicationSampleScope) {
			t.Fatalf("scope=%q 对裁决增量的所有权错误：%v", scope, got)
		}
	}
	if (&apiSampleRecorder{scope: adjudicationSampleScope}).ownsSample("artists/detail.json") {
		t.Fatal("裁决增量不得覆盖原有艺人样本")
	}
}

const adjudicationSampleNames = `
merged-sources-preparing merged-sources-unauthorized merged-sources-forbidden
merged-sources-empty merged-sources merged-sources-after-restore merged-sources-empty-keys
merged-sources-invalid-id merged-sources-not-found
albums-any-preparing albums-default albums-any albums-any-query albums-default-query
albums-any-page albums-any-empty albums-default-after-any
albums-invalid-participation albums-empty-participation albums-repeated-participation
albums-repeated-query albums-long-query albums-invalid-pagination
role-album-artist role-release-artist
tracks-summaries-preparing tracks-summaries tracks-summaries-unknown tracks-summaries-after-merge
tracks-summaries-empty tracks-summaries-invalid-id tracks-summaries-over-limit
tracks-summaries-origin-forbidden tracks-summaries-origin-required tracks-summaries-unauthorized
`
