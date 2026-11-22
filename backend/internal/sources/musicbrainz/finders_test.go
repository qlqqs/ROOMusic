package musicbrainz

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/egress"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

const (
	fixtureMBID = "abcde012-3456-3789-8abc-def012345678"
	creditJSON  = `[{"name":"署名","joinphrase":" & ","artist":{"id":"art","name":"标准名"}}]`
	releaseJSON = `{"id":"rel","title":"发行","date":"2001","country":"JP","status":"Official","barcode":"0123456789012","artist-credit":` + creditJSON + `,
 "label-info":[{"catalog-number":"CAT-1","label":{"name":"厂牌"}}],"release-group":{"id":"grp","title":"专辑","primary-type":"Album","first-release-date":"2000"},
 "media":[{"position":1,"format":"CD","title":"第一碟","track-count":1,"discs":[{"id":"disc-id"}],
 "tracks":[{"position":1,"number":"A1","id":"trk","title":"曲名","length":1010,"artist-credit":` + creditJSON + `,"recording":{"id":"rec","title":"录音","length":2030,"isrcs":["USABC2300001"]}}]}]}`
	recordingJSON = `{"id":"rec","title":"录音","length":2030,"isrcs":["USABC2300001"],"artist-credit":` + creditJSON + `,"releases":[{"id":"rel","title":"发行"}]}`
	artistJSON    = `{"id":"art","name":"标准名","sort-name":"排序","type":"Person","country":"JP","disambiguation":"区别","aliases":[{"name":"别名","primary":true,"locale":"zh"}]}`
)

// 本地服务与适配器走完整 HTTP 链路，每个用例有独立的限速器。
func localOptions(t *testing.T, handler http.HandlerFunc) Options {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return Options{BaseURL: server.URL + "/ws/2", NewClient: func(egress.Options) (*http.Client, error) { return server.Client(), nil }, Limiter: sources.NewLimiter(0, nil, nil)}
}

func queryClient(t *testing.T, path string, params url.Values, status int, body string) *Client {
	t.Helper()
	var hits atomic.Int32
	params.Set("fmt", "json")
	opts := localOptions(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Method != "GET" || r.URL.Path != "/ws/2/"+path || !reflect.DeepEqual(r.URL.Query(), params) {
			t.Errorf("请求 %s %s，期望 GET /ws/2/%s?%s", r.Method, r.URL, path, params.Encode())
		}
		w.WriteHeader(status)
		io.WriteString(w, body)
	})
	t.Cleanup(func() {
		if n := hits.Load(); n != 1 {
			t.Errorf("出站 %d 次，期望 1", n)
		}
	})
	return New(opts)
}

func TestLookupProjections(t *testing.T) {
	ctx := t.Context()
	id := strings.ToUpper(fixtureMBID)
	credits := []sources.Credit{{ArtistID: "art", Name: "署名", JoinPhrase: " & "}}
	t.Run("release", func(t *testing.T) {
		c := queryClient(t, "release/"+fixtureMBID, url.Values{"inc": {"recordings isrcs artist-credits labels release-groups media discids"}}, 200, releaseJSON)
		got, err := c.ReleaseByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		length := int64(1010)
		want := sources.Release{ID: "rel", Title: "发行", Date: "2001", Country: "JP", Status: "Official", Barcode: "0123456789012", Artists: credits, Complete: true,
			Labels: []sources.Label{{Name: "厂牌", CatalogNumber: "CAT-1"}}, Group: sources.Group{ID: "grp", Title: "专辑", PrimaryType: "Album", FirstReleaseDate: "2000"},
			Media: []sources.Medium{{Position: 1, Title: "第一碟", Format: "CD", TrackCount: 1, DiscIDs: []string{"disc-id"}, Tracks: []sources.Track{{ID: "trk", Position: 1, Number: "A1", Title: "曲名", LengthMS: &length, Artists: credits, RecordingID: "rec", RecordingTitle: "录音", ISRCs: []string{"USABC2300001"}}}}}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("发行投影=%+v，期望 %+v", got, want)
		}
	})
	t.Run("recording", func(t *testing.T) {
		c := queryClient(t, "recording/"+fixtureMBID, url.Values{"inc": {"artist-credits isrcs releases"}}, 200, recordingJSON)
		got, err := c.RecordingByID(ctx, id)
		if err != nil || got.ID != "rec" || got.Title != "录音" || got.LengthMS == nil || *got.LengthMS != 2030 || !slices.Equal(got.Artists, credits) || !slices.Equal(got.ISRCs, []string{"USABC2300001"}) || len(got.Releases) != 1 || got.Releases[0].ID != "rel" || got.Releases[0].Complete {
			t.Fatalf("录音投影=%+v %v", got, err)
		}
	})
	t.Run("artist", func(t *testing.T) {
		c := queryClient(t, "artist/"+fixtureMBID, url.Values{"inc": {"aliases"}}, 200, artistJSON)
		got, err := c.ArtistByID(ctx, id)
		want := sources.Artist{ID: "art", Name: "标准名", SortName: "排序", Type: "Person", Country: "JP", Disambiguation: "区别", Aliases: []sources.Alias{{Name: "别名", Locale: "zh", Primary: true}}}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("艺人投影=%+v %v", got, err)
		}
	})
}

func TestSearchQueries(t *testing.T) {
	ctx := t.Context()
	for _, tc := range []struct {
		name, query, limit string
		call               func(*Client) ([]sources.Release, error)
	}{
		{"barcode12", "barcode:123456789012 OR barcode:0123456789012", "25", func(c *Client) ([]sources.Release, error) { return c.ReleasesByBarcode(ctx, "123456789012") }},
		{"barcode13", "barcode:123456789012 OR barcode:0123456789012", "25", func(c *Client) ([]sources.Release, error) { return c.ReleasesByBarcode(ctx, "0123456789012") }},
		{"catalog", `catno:"C\-1" AND label:"A\/B \"C\""`, "25", func(c *Client) ([]sources.Release, error) { return c.ReleasesByCatalog(ctx, "C-1", `A/B "C"`) }},
		{"catalogWithoutLabel", `catno:"C1"`, "25", func(c *Client) ([]sources.Release, error) { return c.ReleasesByCatalog(ctx, "C1", "") }},
		{"release", `release:"a\+b" AND artist:"c\&\&d" AND tracks:12`, "10", func(c *Client) ([]sources.Release, error) {
			return c.SearchReleases(ctx, sources.ReleaseQuery{Title: "a+b", Artist: "c&&d", TrackCount: 12})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := queryClient(t, "release", url.Values{"query": {tc.query}, "limit": {tc.limit}}, 200, `{"releases":[`+releaseJSON+`]}`)
			got, err := tc.call(c)
			if err != nil || len(got) != 1 || got[0].ID != "rel" || got[0].Complete || len(got[0].Media) != 1 || got[0].Media[0].TrackCount != 1 || len(got[0].Media[0].Tracks) != 0 {
				t.Fatalf("搜索投影=%+v %v", got, err)
			}
		})
	}
	t.Run("recording", func(t *testing.T) {
		length := int64(120000)
		c := queryClient(t, "recording", url.Values{"limit": {"10"}, "query": {`recording:"A\?" AND artist:"B" AND dur:[117000 TO 123000]`}}, 200, `{"recordings":[`+recordingJSON+`]}`)
		got, err := c.SearchRecordings(ctx, sources.RecordingQuery{Title: "A?", Artist: "B", LengthMS: &length})
		if err != nil || len(got) != 1 || got[0].ID != "rec" {
			t.Fatalf("录音=%+v %v", got, err)
		}
	})
	t.Run("artist", func(t *testing.T) {
		c := queryClient(t, "artist", url.Values{"query": {`artist:"\\\+\-\&\&\|\|\!\(\)\{\}\[\]\^\"\~\*\?\:\/"`}, "limit": {"10"}}, 200, `{"artists":[`+artistJSON+`]}`)
		got, err := c.SearchArtists(ctx, `\+-&&||!(){}[]^"~*?:/`)
		if err != nil || len(got) != 1 || got[0].ID != "art" {
			t.Fatalf("艺人=%+v %v", got, err)
		}
	})
	for _, tc := range []struct {
		name          string
		status, count int
	}{{"isrc", 200, 2}, {"isrcMissing", 404, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			c := queryClient(t, "isrc/USABC2300001", url.Values{"inc": {"artist-credits releases"}}, tc.status, `{"isrc":"USABC2300001","recordings":[{"id":"r1"},{"id":"r2"}]}`)
			got, err := c.RecordingsByISRC(ctx, " us-Abc-23-00001 ")
			if err != nil || len(got) != tc.count || (len(got) == 2 && (got[0].ID != "r1" || got[1].ID != "r2")) {
				t.Fatalf("ISRC=%+v %v", got, err)
			}
		})
	}
}

func TestInvalidIdentifiers(t *testing.T) {
	c := New(localOptions(t, func(w http.ResponseWriter, r *http.Request) { t.Errorf("非法输入产生出站 %s", r.URL) }))
	ctx := t.Context()
	for _, id := range []string{"", "abcde012-3456-3789-8abc-def01234567Z", strings.Repeat("1", 36)} {
		_, r := c.ReleaseByID(ctx, id)
		_, a := c.ArtistByID(ctx, id)
		_, rec := c.RecordingByID(ctx, id)
		for _, err := range []error{r, a, rec} {
			if !errors.Is(err, sources.ErrInvalidQuery) {
				t.Fatalf("MBID %q：%v", id, err)
			}
		}
	}
	for _, id := range []string{"", "1SABC2300001", "USABC230000X", "USABC23000012"} {
		if _, err := c.RecordingsByISRC(ctx, id); !errors.Is(err, sources.ErrInvalidQuery) {
			t.Fatalf("ISRC %q：%v", id, err)
		}
	}
}

func TestDiscTOC(t *testing.T) {
	const id = "bDCCdX8vZbnGiGD4tcN8aGNdkZc-"
	toc := sources.DiscTOC{Leadout: 267125, Offsets: []int{150, 22717, 41837, 58273, 72053, 91327, 104601, 115341, 132131, 143885, 159823, 174551}}
	if got, err := DiscID(toc); err != nil || got != id {
		t.Fatalf("disc ID=%q %v，期望 %s", got, err, id)
	}
	for _, invalid := range []sources.DiscTOC{{}, {Offsets: make([]int, 100), Leadout: 200}, {Offsets: []int{-1}, Leadout: 200}, {Offsets: []int{150, 150}, Leadout: 200}, {Offsets: []int{151, 150}, Leadout: 200}, {Offsets: []int{150}, Leadout: 150}} {
		if _, err := DiscID(invalid); !errors.Is(err, sources.ErrInvalidQuery) {
			t.Fatalf("非法 TOC %+v：%v", invalid, err)
		}
	}
	for _, tc := range []struct {
		name, body    string
		status, count int
		exact         bool
	}{
		{"exact", `{"id":"` + id + `","sectors":267125,"offset-count":12,"releases":[{"id":"rel"}]}`, 200, 1, true},
		{"fuzzy", `{"release-count":1,"release-offset":0,"releases":[{"id":"rel"}]}`, 200, 1, false},
		{"missing", `{"error":"not found"}`, 404, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := queryClient(t, "discid/"+id, url.Values{"inc": {"artist-credits labels release-groups"}, "cdstubs": {"no"}, "toc": {"1 12 267125 150 22717 41837 58273 72053 91327 104601 115341 132131 143885 159823 174551"}}, tc.status, tc.body)
			got, err := c.ReleasesByDisc(t.Context(), toc)
			if err != nil || got.Exact != tc.exact || len(got.Releases) != tc.count || (tc.count == 1 && (got.Releases[0].ID != "rel" || got.Releases[0].Complete)) {
				t.Fatalf("光盘结果=%+v %v", got, err)
			}
		})
	}
}
