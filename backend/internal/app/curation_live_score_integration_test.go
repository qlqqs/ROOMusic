package app

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

type liveSampleScore struct {
	Outcome     string             `json:"outcome"`
	Accept      []string           `json:"accept"`
	Accepted    bool               `json:"accepted"`
	Title       string             `json:"title"`
	PublicID    string             `json:"expected_release_id"`
	Complete    bool               `json:"complete"`
	Attribution *metricAttribution `json:"failure_attribution,omitempty"`
	Exact       bool               `json:"exact_release"`
	GroupOnly   bool               `json:"group_only"`
	Tracks      int                `json:"scanned_tracks"`
	Reasons     []string           `json:"issues,omitempty"`
}

type liveScores struct {
	Accepted    int               `json:"accepted_samples"`
	Samples     []liveSampleScore `json:"samples"`
	Complete    int               `json:"completed_samples"`
	WrongLinks  int               `json:"wrong_links"`
	WrongMerges int               `json:"wrong_merges"`
	WrongFields int               `json:"wrong_fields"`
	Open        int               `json:"open_items"`
	Attribution map[string]int    `json:"failure_attribution"`
	Stages      map[string]int    `json:"correct_release_matches_by_stage"`
	Resolutions map[string]int    `json:"model_item_outcomes"`
}

func liveCreditText(credits []sources.Credit) string {
	var b strings.Builder
	for _, c := range credits {
		b.WriteString(c.Name)
		b.WriteString(c.JoinPhrase)
	}
	return metadata.NormalizeValue(b.String())
}

// 曲库清单给出发行与发行组。逐轨、署名及字段的真值取自生成这些静音文件时保存的同一份公开响应。
func liveCorpusScore(t *testing.T, app *integrationTestApplication, entries []liveAlbum, routes map[string][]string) liveScores {
	t.Helper()
	data, err := os.ReadFile(os.Getenv("ROOMUSIC_LIVE_REFERENCE"))
	requireCurationOK(t, err)
	var releases []sources.Release
	requireCurationOK(t, json.Unmarshal(data, &releases))
	ref := map[string]sources.Release{}
	artistIDs := map[string][]string{}
	indexCredits := func(credits []sources.Credit) {
		for _, c := range credits {
			name := metadata.NormalizeValue(c.Name)
			if !slices.Contains(artistIDs[name], c.ArtistID) {
				artistIDs[name] = append(artistIDs[name], c.ArtistID)
			}
		}
	}
	for _, r := range releases {
		ref[r.ID] = r
		indexCredits(r.Artists)
		for _, m := range r.Media {
			for _, track := range m.Tracks {
				indexCredits(track.Artists)
			}
		}
	}
	for _, album := range entries {
		if album.Tags == "romanized" {
			name := metadata.NormalizeValue(album.LocalArtist)
			for _, credit := range ref[album.ID].Artists {
				if metadata.NormalizeValue(credit.Name) == metadata.NormalizeValue(album.Artist) {
					artistIDs[name] = append(artistIDs[name], credit.ArtistID)
				}
			}
		}
	}
	links := metricStoredLinks(t, app)
	fields := metricStoredFields(t, app)
	suggestions := metricSuggestions(t, app)
	states := map[string]string{}
	metricReadRows(t, app, `SELECT target_id,status FROM curation_work_states WHERE subject='release'`, func(rows *sql.Rows) {
		var id, status string
		requireCurationOK(t, rows.Scan(&id, &status))
		states[id] = status
	})
	localCleanup := map[string]bool{}
	metricReadRows(t, app, `SELECT release_id FROM release_auto_values WHERE writer_level=1
		UNION SELECT m.release_id FROM track_auto_values v JOIN tracks t ON t.id=v.track_id
		JOIN media m ON m.id=t.medium_id WHERE v.writer_level=1`, func(rows *sql.Rows) {
		var id string
		requireCurationOK(t, rows.Scan(&id))
		localCleanup[id] = true
	})
	s := liveScores{Stages: map[string]int{"program": 0, "select": 0, "lookup": 0, "web": 0}, Resolutions: map[string]int{}}
	needs := make([][]metricNeed, len(entries))
	need := func(i int, obj, kind, value string) {
		n := metricNeed{obj, kind, value}
		if !slices.Contains(needs[i], n) {
			needs[i] = append(needs[i], n)
		}
	}
	owner := map[string][]int{}
	own := func(id string, i int) {
		if !slices.Contains(owner[id], i) {
			owner[id] = append(owner[id], i)
		}
	}
	rejected := make([]bool, len(entries))
	issue := func(i int, msg string) {
		rejected[i] = true
		s.Samples[i].Reasons = append(s.Samples[i].Reasons, msg)
	}
	checkFields := func(i int, obj, target string, expected map[string][]string) {
		for field, v := range fields[obj] {
			if !slices.Contains(expected[field], metadata.NormalizeValue(v.value)) {
				s.WrongFields++
				if values := expected[field]; len(values) > 0 {
					need(i, obj, field, values[0])
				}
				issue(i, fmt.Sprintf("错改 %s.%s：%q，期望 %q", target, field, v.value, expected[field]))
			}
		}
	}
	countedLinks := map[string]bool{}
	checkLinks := func(i int, kind, obj, target string, expected []string) bool {
		found := false
		for _, l := range links[metricLinkSubject{kind, obj}] {
			if l.sourceID == "musicbrainz" && slices.Contains(expected, l.value) {
				found = true
				continue
			}
			if !countedLinks[l.row] {
				s.WrongLinks++
				countedLinks[l.row] = true
			}
			if len(expected) > 0 {
				need(i, obj, kind, expected[0])
			}
			issue(i, fmt.Sprintf("错连 %s/%s：%s，期望 %v", target, kind, l.value, expected))
		}
		return found
	}
	groupOwners := map[string][]int{}
	byDir := map[string]int{}
	wantTracks := map[string]sources.Track{}
	matchStage := map[string]string{}
	for i, e := range entries {
		r, ok := ref[e.ID]
		if !ok || r.Group.ID != e.Group {
			t.Fatalf("生成明细与清单不同：%s", e.ID)
		}
		want := e.Expect
		if len(want.Accept) == 0 {
			// 旧公开清单的评分接受正确的版本或发行组对应。
			want.Accept = []string{"release", "release_group"}
		}
		want = want.withOutcomeRules()
		s.Samples = append(s.Samples, liveSampleScore{Title: e.Title, PublicID: e.ID, Accept: want.Accept})
		byDir[e.Directory] = i
		publicTracks := map[[2]int]sources.Track{}
		for _, m := range r.Media {
			for _, tr := range m.Tracks {
				publicTracks[[2]int{m.Position, tr.Position}] = tr
				if e.GeneratedTracks == nil {
					wantTracks[fmt.Sprintf("%s/%02d-%02d", e.Directory, m.Position, tr.Position)] = tr
				}
			}
		}
		for _, file := range e.GeneratedTracks {
			tr, exists := publicTracks[[2]int{file.Disc, file.Position}]
			if file.Synthetic {
				tr = sources.Track{Title: file.Title, Artists: r.Artists}
			} else if !exists {
				t.Fatalf("生成的 %s 无法按碟序和曲序对应公开参考", file.File)
			}
			wantTracks[path.Join(e.Directory, file.File)] = tr
		}
		if len(e.ReleaseIDs) != 1 {
			issue(i, fmt.Sprintf("同一输入目录扫出 %d 个发行", len(e.ReleaseIDs)))
		}
		expected := map[string][]string{"title": {r.Title}, "album_artist": {liveCreditText(r.Artists)}, "barcode": {r.Barcode}}
		if len(r.Date) >= 4 {
			expected["year"] = []string{r.Date[:4]}
		}
		for _, l := range r.Labels {
			expected["label"] = append(expected["label"], l.Name)
			expected["catalog_number"] = append(expected["catalog_number"], l.CatalogNumber)
		}
		partial := e.Tags == "subset" || e.Tags == "superset"
		if partial {
			// 组级部分吻合不给版本字段提供写入依据，即使碰巧与参考版本同值。
			for _, field := range []string{"year", "label", "barcode", "catalog_number"} {
				delete(expected, field)
			}
		}
		for _, id := range e.ReleaseIDs {
			own(id, i)
			var group, title string
			requireCurationOK(t, app.database.QueryRowContext(t.Context(), `SELECT group_id,title FROM release_effective WHERE id=$1`, id).Scan(&group, &title))
			groupOwners[group] = append(groupOwners[group], i)
			exact := checkLinks(i, "release", id, e.Title, []string{e.ID})
			directGroup := checkLinks(i, "release_group_match", id, e.Title, []string{e.Group})
			checkLinks(i, "release_group", group, e.Title, []string{e.Group})
			score := &s.Samples[i]
			if exact && partial {
				s.WrongLinks++
				issue(i, "部分曲序只允许组级对应，实际写入了版本对应")
			}
			switch {
			case len(links[metricLinkSubject{"release", id}]) > 0:
				score.Outcome = "release"
			case len(links[metricLinkSubject{"release_group_match", id}]) > 0:
				score.Outcome = "release_group"
			case len(suggestions[id]) > 0:
				score.Outcome = "suggestion"
			case localCleanup[id]:
				score.Outcome = "local_cleanup"
			default:
				score.Outcome = states[id]
			}
			for _, problem := range metricSuggestionIssues(t, e.Title, want, suggestions[id]) {
				issue(i, fmt.Sprintf("%s %s，期望 %s，实际 %s", problem.Kind, problem.Field, problem.Expected, problem.Actual))
			}
			s.Samples[i].Exact = s.Samples[i].Exact || exact
			s.Samples[i].GroupOnly = s.Samples[i].GroupOnly || (!exact && directGroup)
			if exact || directGroup {
				matchStage[id] = "program"
			} else {
				if partial {
					need(i, id, "group", e.Group)
				} else {
					need(i, id, "release", e.ID)
				}
				s.Samples[i].Reasons = append(s.Samples[i].Reasons, "缺少清单中的发行或发行组对应")
			}
			if metadata.NormalizeValue(title) != metadata.NormalizeValue(r.Title) && !(e.Tags == "romanized" && metadata.NormalizeValue(title) == metadata.NormalizeValue(e.LocalTitle)) {
				need(i, id, "title", r.Title)
				issue(i, fmt.Sprintf("专辑显示名 %q，期望 %q", title, r.Title))
			}
			checkFields(i, id, e.Title, expected)
		}
	}
	metricReadRows(t, app, `SELECT t.id,t.relative_path,coalesce(m.origin_release_id,m.release_id) FROM tracks t JOIN media m ON m.id=t.medium_id WHERE t.source_status='present'`, func(rows *sql.Rows) {
		var id, file, release string
		requireCurationOK(t, rows.Scan(&id, &file, &release))
		dir := path.Dir(file)
		i, ok := byDir[dir]
		key := file
		if ok && entries[i].GeneratedTracks == nil {
			prefix, _, _ := strings.Cut(path.Base(file), " - ")
			key = path.Join(dir, prefix)
		}
		tr, known := wantTracks[key]
		if !ok || !known {
			t.Fatalf("扫描得到不在生成清单中的曲目或重复曲目：%s", file)
		}
		delete(wantTracks, key)
		own(id, i)
		s.Samples[i].Tracks++
		var recordings []string
		if tr.RecordingID != "" && entries[i].Tags != "subset" && entries[i].Tags != "superset" {
			recordings = []string{tr.RecordingID}
		}
		found := checkLinks(i, "track", id, file, recordings)
		if s.Samples[i].Exact && !found {
			need(i, release, "recording", tr.RecordingID)
			s.Samples[i].Reasons = append(s.Samples[i].Reasons, "已对到发行但缺少录音对应："+file)
		}
		credits := tr.Artists
		if len(credits) == 0 {
			credits = ref[entries[i].ID].Artists
		}
		title := tr.Title
		if title == "" {
			title = tr.RecordingTitle
		}
		checkFields(i, id, file, map[string][]string{"title": {metadata.NormalizeValue(title)}, "artist": {liveCreditText(credits)}})
	})
	for key := range wantTracks {
		issue(byDir[path.Dir(key)], "扫描缺曲："+key)
	}
	metricReadRows(t, app, `SELECT DISTINCT c.artist_id,n.spelling,coalesce(m.origin_release_id,m.release_id,c.release_id)
 FROM artist_effective_credits c JOIN artist_names n ON c.name_id=n.id
 LEFT JOIN tracks t ON t.id=c.track_id LEFT JOIN media m ON m.id=t.medium_id`, func(rows *sql.Rows) {
		var id, name, release string
		requireCurationOK(t, rows.Scan(&id, &name, &release))
		for _, i := range owner[release] {
			own(id, i)
		}
	})
	artistNames := map[string]string{}
	metricReadRows(t, app, `SELECT id,initial_name FROM artists`, func(rows *sql.Rows) {
		var id, name string
		requireCurationOK(t, rows.Scan(&id, &name))
		artistNames[id] = metadata.NormalizeValue(name)
		for _, i := range owner[id] {
			checkLinks(i, "artist", id, name, artistIDs[artistNames[id]])
		}
	})
	for _, members := range groupOwners {
		for j, i := range members {
			for _, peer := range members[:j] {
				if entries[i].Group != entries[peer].Group {
					s.WrongMerges++
					issue(i, "错归组："+entries[peer].Title)
					issue(peer, "错归组："+entries[i].Title)
				}
			}
		}
	}
	metricReadRows(t, app, `SELECT source_release_id,target_release_id FROM release_merges`, func(rows *sql.Rows) {
		var a, b string
		requireCurationOK(t, rows.Scan(&a, &b))
		s.WrongMerges++
		for _, id := range []string{a, b} {
			for _, i := range owner[id] {
				issue(i, "清单未标注的发行合并")
			}
		}
	})
	metricReadRows(t, app, `SELECT artist_id,redirect_artist_id FROM artist_overrides WHERE redirect_artist_id IS NOT NULL`, func(rows *sql.Rows) {
		var a, b string
		requireCurationOK(t, rows.Scan(&a, &b))
		for _, id := range artistIDs[artistNames[a]] {
			if slices.Contains(artistIDs[artistNames[b]], id) {
				return
			}
		}
		s.WrongMerges++
		for _, id := range []string{a, b} {
			for _, i := range owner[id] {
				issue(i, "错并艺人："+artistNames[a]+" / "+artistNames[b])
			}
		}
	})
	metricReadRows(t, app, `SELECT id,coalesce(target_id::text,artist_id::text,''),kind,status,coalesce(resolution,'') FROM curation_pending_items`, func(rows *sql.Rows) {
		var id, obj, kind, status, res string
		requireCurationOK(t, rows.Scan(&id, &obj, &kind, &status, &res))
		stage := "unclaimed"
		if history := routes[id]; len(history) > 0 {
			stage = history[len(history)-1]
		}
		s.Resolutions[stage+"/"+kind+"/"+status+"/"+res]++
		if status == "waiting_model" || status == "in_progress" || status == "waiting_human" || status == "waiting_route" {
			s.Open++
			for _, i := range owner[obj] {
				issue(i, "待定项未结："+kind+"/"+status)
			}
		}
		if kind == "release_match" && res == "model_matched" && matchStage[obj] != "" {
			matchStage[obj] = stage
		}
	})
	for _, stage := range matchStage {
		s.Stages[stage]++
	}
	history := metricHistories(t, app)
	s.Attribution = map[string]int{"info_missing": 0, "source_unavailable": 0, "model_judgment": 0}
	for object, runs := range history {
		for _, run := range runs {
			if run.closedUnavailable() {
				for _, i := range owner[object] {
					issue(i, "来源不可用时结案："+run.Kind+"/"+run.Resolution)
				}
			}
		}
	}
	for i := range s.Samples {
		score := &s.Samples[i]
		if !slices.Contains(score.Accept, score.Outcome) {
			issue(i, fmt.Sprintf("终态 %q，允许 %v", score.Outcome, score.Accept))
		}
		score.Accepted = !rejected[i]
		if score.Accepted {
			s.Accepted++
		}
		slices.Sort(score.Reasons)
		score.Reasons = slices.Compact(score.Reasons)
		score.Complete = len(score.Reasons) == 0
		if score.Complete {
			s.Complete++
		}
		if metricNeedsAttribution(metricDetail{Accept: score.Accept, Outcome: score.Outcome, Accepted: score.Accepted, Complete: score.Complete}) {
			for _, id := range entries[i].ReleaseIDs {
				if len(needs[i]) == 0 {
					need(i, id, "release", entries[i].ID)
				}
			}
			score.Attribution = metricAttribute(needs[i], history, "")
			s.Attribution[score.Attribution.Category]++
		}
	}
	return s
}

func TestPostgreSQLCurationLiveAttributionScope(t *testing.T) {
	samples := loadMetricSamples(t)
	index := slices.IndexFunc(samples, func(s metricSample) bool { return s.ID == "spoken-book" })
	if index < 0 {
		t.Fatal("缺少合成的朗读样本")
	}
	sample := samples[index]
	ref := sources.Release{ID: uuid.New(), Group: sources.Group{ID: uuid.New()}, Title: sample.Local.Album,
		Artists: []sources.Credit{{ArtistID: uuid.New(), Name: sample.Local.Artist}}, Media: []sources.Medium{{Position: 1}}}
	for i, track := range sample.Local.Tracks {
		sample.Local.Tracks[i].File = fmt.Sprintf("01-%02d - %s.flac", i+1, track.Title)
		ref.Media[0].Tracks = append(ref.Media[0].Tracks, sources.Track{Position: i + 1, Title: track.Title, RecordingID: uuid.New()})
	}
	app, entries, endpoint := runMetricSamples(t, []metricSample{sample})
	file := filepath.Join(t.TempDir(), "合成参考.json")
	raw, err := json.Marshal([]sources.Release{ref})
	requireCurationOK(t, err)
	requireCurationOK(t, os.WriteFile(file, raw, 0644))
	t.Setenv("ROOMUSIC_LIVE_REFERENCE", file)
	album := liveAlbum{ID: ref.ID, Group: ref.Group.ID, Title: ref.Title, Directory: sample.Local.Directory, ReleaseIDs: []string{entries[0].release}, Expect: sample.Expect}
	endpoint.mu.Lock()
	defer endpoint.mu.Unlock()
	safe := liveCorpusScore(t, app, []liveAlbum{album}, endpoint.routes)
	if safe.Accepted != 1 || safe.Samples[0].Outcome != "unlisted" || safe.Samples[0].Attribution != nil {
		t.Fatalf("正确未收录的试用样本被归因：%+v", safe)
	}
	// 同一份运行只改检验标注，核实需要外部编号时才检查能否取得它。
	album.Expect.Accept = []string{"release"}
	incomplete := liveCorpusScore(t, app, []liveAlbum{album}, endpoint.routes)
	if incomplete.Accepted != 0 || incomplete.Attribution["info_missing"] != 1 || incomplete.Samples[0].Attribution == nil {
		t.Fatalf("需要外部编号的试用样本缺少逐项归因：%+v", incomplete)
	}
}
