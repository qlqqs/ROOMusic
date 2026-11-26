package app

import (
	"net/http"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/upstreammock"
)

func TestPostgreSQL整理模型提交同组多版须有版本标识(t *testing.T) {
	artist := metricArtist{ID: uuid.New(), Name: "合成艺人"}
	s := cMatchAlbum("合成专辑", artist.Name, 5, artist.ID)
	s.External.Artists = []metricArtist{artist}
	first := s.External.Releases[0]
	first.Label, first.Catalog, first.Barcode = "甲厂牌", "A-123", "0123456789012"
	second := cMatchAlbum(s.Local.Album, artist.Name, 5, artist.ID).External.Releases[0]
	second.Group, second.Label, second.Catalog, second.Barcode = first.Group, "乙厂牌", "B-123", "0987654321012"
	s.External.Releases = []metricRelease{first, second}

	for _, tc := range []struct {
		name   string
		tagged bool
	}{
		{"仅结构吻合时保留发行组", false},
		{"本地版本编号吻合时写入具体版本", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock, err := upstreammock.New(upstreammock.Options{DataDir: writeMetricCatalog(t, []metricSample{s})})
			requireCurationOK(t, err)
			lookup := &testLookupGate{}
			lookup.ready.Store(true)
			source := http.StripPrefix("/mb", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r.URL.Path = "/musicbrainz/ws/2" + r.URL.Path
				mock.ServeHTTP(w, r)
			}))
			f := newSchedulerFixtureWithSources(t, lookup, source)
			app := f.application
			target, tracks := b3ReleaseTracks(t, app, f.rootID, s.Local.Album, artist.Name, 5)
			for i, track := range tracks {
				mustExec(t, app, `UPDATE tracks SET title=$2,duration_seconds=$3 WHERE id=$1`, track, s.Local.Tracks[i].Title, s.Local.Tracks[i].Seconds)
			}
			if tc.tagged {
				programTag(t, app, tracks[0], "mb_release", first.ID)
			}

			// 第二档提交一个编号，同组的另一版只出现在 MusicBrainz 的发行组详情中。
			item := modelRouteFixture(t, f, f.pending(t, target), "lookup")
			mustExec(t, app, `UPDATE curation_pending_items SET candidates='[]' WHERE id=$1`, item.ID)
			f.configure(t, &f.admin)
			f.enable(t)
			f.next(t, false).tool(t, "resolve_release_match", map[string]any{
				"item_id": item.ID, "external_id": first.ID, "basis": map[string]string{"note": "五首曲目位置与时长匹配"},
			})
			modelWaitResolution(t, f, item.ID, "model_matched")
			b3RequireCount(t, app, 0, `SELECT count(*) FROM steward_reviews`)
			if tc.tagged {
				b3RequireCount(t, app, 0, `SELECT count(*) FROM external_links WHERE object_type='release_group_match' AND object_id=$1`, target)
				b3RequireCount(t, app, 1, `SELECT count(*) FROM external_links WHERE object_type='release' AND object_id=$1 AND external_id=$2`, target, first.ID)
				for i, track := range tracks {
					b3RequireCount(t, app, 1, `SELECT count(*) FROM external_links WHERE object_type='track' AND object_id=$1 AND external_id=$2`, track, first.Tracks[i].ID)
				}
				for field, value := range map[string]string{"barcode": first.Barcode, "catalog_number": first.Catalog, "label": first.Label} {
					b3RequireCount(t, app, 1, `SELECT count(*) FROM release_auto_values WHERE release_id=$1 AND field=$2 AND text_value=$3`, target, field, value)
				}
				return
			}
			b3RequireCount(t, app, 1, `SELECT count(*) FROM external_links WHERE object_type='release_group_match' AND object_id=$1 AND external_id=$2`, target, first.Group)
			b3RequireCount(t, app, 0, `SELECT count(*) FROM external_links WHERE object_type='release' AND object_id=$1`, target)
			b3RequireCount(t, app, 0, `SELECT count(*) FROM external_links WHERE object_type='track' AND object_id=ANY($1::uuid[])`, tracks)
			b3RequireCount(t, app, 0, `SELECT count(*) FROM release_auto_values WHERE release_id=$1 AND field IN ('barcode','catalog_number','label')`, target)
			checkedOther := false
			for _, r := range cMatchRequests(t, mock) {
				if r.Path == "/musicbrainz/ws/2/release/"+second.ID {
					checkedOther = true
				}
			}
			if !checkedOther {
				t.Fatal("未核对同组另一版的曲目详情")
			}
		})
	}
}
