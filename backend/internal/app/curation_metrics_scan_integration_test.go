package app

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/upstreammock"
)

// 这些是特定样本的必需中间证据；最终对应相同，仍可能掩盖错误分碟、错用占位值或跳过模型的问题。
type metricScanExpect struct {
	Title             string
	TitleAuth         string `json:"title_auth"`
	Artist            string
	ArtistAuth        string `json:"artist_auth"`
	LocalTitle        string `json:"local_title"`
	Compilation       *bool
	Positions         []string
	InferredPositions bool   `json:"inferred_positions"`
	TrackTitleAuth    string `json:"track_title_auth"`
	Decodings         []string
	RequireMatchItem  bool     `json:"require_match_item"`
	RequireLocalItem  bool     `json:"require_local_item"`
	RelaxedTitle      string   `json:"relaxed_title"`
	ArtistRetry       string   `json:"artist_retry"`
	ForbiddenQuery    []string `json:"forbidden_query"`
}

type metricSourceQuery struct {
	service, field, value string
}

func metricQueriesSince(t *testing.T, mock *upstreammock.Server, since time.Time) []metricSourceQuery {
	t.Helper()
	w := httptest.NewRecorder()
	mock.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/_mock/requests?limit=500", nil))
	var records []struct {
		Time    time.Time
		Service string
		Query   url.Values
	}
	requireCurationOK(t, json.Unmarshal(w.Body.Bytes(), &records))
	slices.Reverse(records)
	var queries []metricSourceQuery
	for _, record := range records {
		if record.Time.Before(since) {
			continue
		}
		var fields []string
		switch record.Service {
		case "musicbrainz":
			fields = []string{"query"}
		case "searxng":
			fields = []string{"q"}
		case "lrclib":
			fields = []string{"album_name", "artist_name", "track_name"}
		}
		for _, field := range fields {
			if value := record.Query.Get(field); value != "" {
				queries = append(queries, metricSourceQuery{record.Service, field, value})
			}
		}
	}
	return queries
}

func metricFixtureIssues(t *testing.T, app *integrationTestApplication, e metricEntry) []metricIssue {
	t.Helper()
	want := e.sample.Expect.Scan
	if want == nil {
		return nil
	}
	evs, err := catalog.LoadCurationEvidence(t.Context(), app.database, []string{e.release})
	requireCurationOK(t, err)
	ev := evs[e.release]
	var issues []metricIssue
	check := func(field, expected, actual string) {
		if expected != "" && expected != actual {
			issues = append(issues, metricIssue{Kind: "scan_field", Target: e.sample.ID, Field: field, Expected: expected, Actual: actual})
		}
	}
	check("title", want.Title, ev.Title)
	check("title_auth", want.TitleAuth, string(ev.Authenticity["title"]))
	check("artist", want.Artist, cmp.Or(ev.AlbumArtist, ev.Artist))
	check("artist_auth", want.ArtistAuth, string(ev.SelectedArtistAuth()))
	check("local_title", want.LocalTitle, ev.LocalTitle)
	if want.LocalTitle != "" {
		var level int
		requireCurationOK(t, app.database.QueryRowContext(t.Context(), `SELECT coalesce(max(writer_level),0) FROM release_auto_values WHERE release_id=$1 AND field='title' AND text_value=$2`, e.release, want.LocalTitle).Scan(&level))
		check("local_title_writer_level", "1", fmt.Sprint(level))
	}
	if want.Compilation != nil {
		check("compilation", fmt.Sprint(*want.Compilation), fmt.Sprint(ev.Compilation))
	}
	for _, track := range ev.Tracks {
		i := slices.Index(e.tracks, track.ID)
		if i < 0 {
			continue
		}
		file := e.sample.Local.Tracks[i].File
		if len(want.Positions) > 0 {
			check(file+"/position", want.Positions[i], fmt.Sprintf("%d/%d", track.Disc, track.Position))
		}
		if want.InferredPositions {
			check(file+"/inferred", "true", fmt.Sprint(track.PositionInferred))
		}
		check(file+"/title_auth", want.TrackTitleAuth, string(track.Authenticity["title"]))
	}
	if want.RequireLocalItem || want.RequireMatchItem {
		kinds := []string{"local_cleanup"}
		if want.RequireMatchItem {
			kinds = []string{"recording_match", "release_match"}
		}
		var count int
		requireCurationOK(t, app.database.QueryRowContext(t.Context(), `SELECT count(*) FROM curation_pending_items WHERE target_id=$1 AND kind=ANY($2) AND conversation_id IS NOT NULL`, e.release, kinds).Scan(&count))
		check("model_item/"+kinds[0], "true", fmt.Sprint(count > 0))
	}
	for _, candidate := range want.Decodings {
		var found bool
		requireCurationOK(t, app.database.QueryRowContext(t.Context(), `SELECT EXISTS(SELECT FROM curation_pending_items p WHERE p.target_id=$1 AND kind='local_cleanup' AND jsonb_path_exists(evidence,'$.** ? (@ == $v)',jsonb_build_object('v',$2::text)))`, e.release, candidate).Scan(&found))
		check("local_decoding/"+candidate, "true", fmt.Sprint(found))
	}
	if want.RelaxedTitle != "" {
		full, clean := false, false
		for _, request := range e.queries {
			if request.service != "musicbrainz" {
				continue
			}
			query := request.value
			// 第二档也能按标题搜索；程序的放宽查询还包含艺人条件。
			if !strings.Contains(query, " AND artist:") && !strings.Contains(query, " AND arid:") {
				continue
			}
			if strings.Contains(query, e.sample.Local.Album) {
				full = true
			}
			if full && strings.Contains(query, fmt.Sprintf("release:%q", want.RelaxedTitle)) {
				clean = true
			}
		}
		check("relaxed_title", "true", fmt.Sprint(clean))
	}
	if want.ArtistRetry != "" {
		byID, byName := false, false
		for _, request := range e.queries {
			q := request.value
			if request.service != "musicbrainz" || !strings.Contains(q, e.sample.Local.Album) {
				continue
			}
			if strings.Contains(q, "arid:") && strings.Contains(q, want.ArtistRetry) {
				byID = true
			}
			if byID && strings.Contains(q, "artist:") && strings.Contains(q, e.sample.Local.Artist) {
				byName = true
			}
		}
		check("arid_then_name", "true", fmt.Sprint(byName))
	}
	for _, value := range want.ForbiddenQuery {
		seen := map[string]bool{}
		for _, request := range e.queries {
			field := request.service + "/" + request.field
			if !seen[field] && strings.Contains(strings.ToLower(request.value), strings.ToLower(value)) {
				check("forbidden_query/"+field, "不得包含 "+value, request.value)
				seen[field] = true
			}
		}
	}
	return issues
}

func TestCurationLiveGapSampleCoverage(t *testing.T) {
	shapes := []string{
		"multidisc", "subset", "superset", "single_b_side", "noisy_directory", "legacy_encoding",
		"joint_credit", "compilation_no_albumartist", "duplicate_copy", "mixed_directory",
		"live_duration_conflict", "vinyl_sides", "hidden_track", "translated_album",
		"english_placeholder", "spoken_audio", "external_namesake", "group_title_typo", "model_ambiguous_editions",
	}
	outcomes := []string{"release", "release_group", "unlisted", "no_usable_candidates", "suggestion", "local_cleanup"}
	counts, ids := map[string]int{}, map[string]bool{}
	for _, sample := range loadMetricSamples(t) {
		if ids[sample.ID] {
			t.Errorf("重复的样本编号 %s", sample.ID)
		}
		ids[sample.ID] = true
		if !slices.Contains(shapes, sample.Shape) {
			continue
		}
		counts[sample.Shape]++
		if len(sample.Expect.Accept) == 0 {
			t.Errorf("%s 缺少允许的最终结果", sample.ID)
		}
		for _, value := range sample.Expect.Accept {
			if !slices.Contains(outcomes, value) {
				t.Errorf("%s 的允许结果 %q 不在合同内", sample.ID, value)
			}
		}
		if sample.Shape == "live_duration_conflict" && (sample.Expect.SuggestionCanAccept == nil || *sample.Expect.SuggestionCanAccept || !slices.Equal(sample.Expect.Accept, []string{"unlisted", "suggestion"})) {
			t.Errorf("%s 的现场版时长反证只允许未收录或不可采纳的建议", sample.ID)
		}
		if scan := sample.Expect.Scan; scan != nil && len(scan.Positions) > 0 && len(scan.Positions) != len(sample.Local.Tracks) {
			t.Errorf("%s 的碟号与曲位标注未覆盖全部本地音轨", sample.ID)
		}
	}
	for _, shape := range shapes {
		if counts[shape] < 2 {
			t.Errorf("%s 仅有 %d 份样本，至少需要两份", shape, counts[shape])
		}
	}
}
