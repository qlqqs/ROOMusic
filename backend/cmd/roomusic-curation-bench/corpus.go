package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/qlqq/roomusic/backend/internal/platform/egress"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/musicbrainz"
)

type corpusOptions struct {
	out, ffmpeg, reference, referenceIn, ids, trackMap string
}

func generateCorpus(ctx context.Context, corpus []corpusEntry, options corpusOptions) error {
	out, ffmpeg := options.out, options.ffmpeg
	if options.trackMap == "" {
		options.trackMap = filepath.Clean(out) + "-tracks.json"
	}
	selected := map[string]bool{}
	if options.ids != "" {
		for id := range strings.SplitSeq(options.ids, ",") {
			id = strings.TrimSpace(id)
			if !slices.ContainsFunc(corpus, func(e corpusEntry) bool { return e.ID == id }) {
				return fmt.Errorf("发行不在清单中：%q", id)
			}
			selected[id] = true
		}
	}
	lookup, err := corpusLookup(options.referenceIn)
	if err != nil {
		return err
	}
	if ffmpeg == "" {
		ffmpeg = "ffmpeg"
	}
	binary, err := exec.LookPath(ffmpeg)
	if err != nil {
		return fmt.Errorf("找不到 ffmpeg：%w", err)
	}
	if err := os.MkdirAll(out, 0755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(out, ".curation-bench-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	var releases []sources.Release
	var albums []albumMap
	totalTracks := 0
	for i, entry := range corpus {
		if len(selected) != 0 && !selected[entry.ID] {
			continue
		}
		artist, title := entry.Artist, entry.Title
		if entry.Tags == "romanized" {
			artist, title = entry.LocalArtist, entry.LocalTitle
		}
		name := fmt.Sprintf("%02d - %s", i+1, fileLabel(artist+" - "+title))
		final := filepath.Join(out, name)
		if _, err := os.Lstat(final); !errors.Is(err, os.ErrNotExist) {
			if err != nil {
				return err
			}
			return fmt.Errorf("曲库输出已存在：%s", final)
		}
		release, err := lookup(ctx, entry.ID)
		if err != nil {
			return fmt.Errorf("读取 %s (%s)：%w", entry.Title, entry.ID, err)
		}
		dir := filepath.Join(stage, name)
		if err := os.Mkdir(dir, 0755); err != nil {
			return err
		}
		tracks, err := planTracks(entry, release)
		if err != nil {
			return fmt.Errorf("生成 %s (%s)：%w", entry.Title, entry.ID, err)
		}
		for _, track := range tracks {
			if err := writeTrack(ctx, binary, dir, track); err != nil {
				return fmt.Errorf("生成 %s：%w", entry.Title, err)
			}
		}
		totalTracks += len(tracks)
		albums = append(albums, albumMap{entry.ID, name, tracks})
		releases = append(releases, release)
	}
	if options.reference != "" {
		if err := writeJSONFile(options.reference, releases); err != nil {
			return err
		}
	}
	if err := writeJSONFile(options.trackMap, albums); err != nil {
		return err
	}
	// 全部音频编码成功才将专辑移到曲库中，失败的编码和远端读取可直接重试。
	for _, a := range albums {
		if err := os.Rename(filepath.Join(stage, a.Directory), filepath.Join(out, a.Directory)); err != nil {
			return err
		}
	}
	return writeResult(map[string]any{"directory": out, "releases": len(albums), "tracks": totalTracks})
}

func writeJSONFile(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}

func corpusLookup(reference string) (func(context.Context, string) (sources.Release, error), error) {
	if reference != "" {
		data, err := os.ReadFile(reference)
		if err != nil {
			return nil, err
		}
		var releases []sources.Release
		if err = json.Unmarshal(data, &releases); err != nil {
			return nil, err
		}
		byID := map[string]sources.Release{}
		for _, r := range releases {
			byID[r.ID] = r
		}
		return func(_ context.Context, id string) (sources.Release, error) {
			if r, ok := byID[id]; ok {
				return r, nil
			}
			return sources.Release{}, fmt.Errorf("公开参考中缺少发行 %s", id)
		}, nil
	}
	opts := musicbrainz.Options{}
	mbURL := &url.URL{Scheme: "https", Host: "musicbrainz.org"}
	proxy, err := http.ProxyFromEnvironment(&http.Request{URL: mbURL})
	if err != nil {
		return nil, err
	}
	if proxy != nil {
		// bench 只读取清单中固定 MusicBrainz 主机的公开发行，不跟随重定向。
		opts.NewClient = func(o egress.Options) (*http.Client, error) {
			return &http.Client{Timeout: o.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			}}, nil
		}
	}
	return musicbrainz.New(opts).ReleaseByID, nil
}

func fileLabel(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '/' || r == '\\' || unicode.IsControl(r) {
			r = '-'
		}
		// 给盘序、曲序与扩展名留出空间，按字节限制并保留完整字符。
		if b.Len()+len(string(r)) > 180 {
			break
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

func creditTags(credits []sources.Credit) (string, string) {
	var name strings.Builder
	var ids []string
	for _, c := range credits {
		name.WriteString(c.Name)
		name.WriteString(c.JoinPhrase)
		if c.ArtistID != "" {
			ids = append(ids, c.ArtistID)
		}
	}
	return name.String(), strings.Join(ids, "; ")
}

func trackTags(entry corpusEntry, r sources.Release, m sources.Medium, t sources.Track) map[string]string {
	if entry.Tags == "untagged" {
		return nil
	}
	title := t.Title
	if title == "" {
		title = t.RecordingTitle
	}
	album := r.Title
	if entry.Tags == "typo" {
		album = entry.LocalTitle
	}
	releaseArtist, releaseIDs := creditTags(r.Artists)
	artist, artistIDs := creditTags(t.Artists)
	if artist == "" {
		artist, artistIDs = releaseArtist, releaseIDs
	}
	fields := map[string]string{
		"TITLE": title, "ARTIST": artist, "ALBUM": album, "ALBUMARTIST": releaseArtist,
		"DATE": r.Date, "TRACKNUMBER": strconv.Itoa(t.Position), "DISCNUMBER": strconv.Itoa(m.Position), "MEDIA": m.Format,
	}
	if len(r.Labels) > 0 {
		fields["LABEL"] = r.Labels[0].Name
	}
	if entry.Tags == "picard" || entry.Tags == "no_barcode" {
		fields["MUSICBRAINZ_ALBUMID"] = r.ID
		fields["MUSICBRAINZ_RELEASEGROUPID"] = r.Group.ID
		fields["MUSICBRAINZ_TRACKID"] = t.RecordingID
		fields["MUSICBRAINZ_RELEASETRACKID"] = t.ID
		fields["MUSICBRAINZ_ARTISTID"] = artistIDs
		fields["MUSICBRAINZ_ALBUMARTISTID"] = releaseIDs
		fields["ISRC"] = strings.Join(t.ISRCs, "; ")
		if len(r.Labels) > 0 {
			fields["CATALOGNUMBER"] = r.Labels[0].CatalogNumber
		}
	}
	if entry.Tags == "picard" {
		fields["BARCODE"] = r.Barcode
	}
	for k, v := range fields {
		if v == "" {
			delete(fields, k)
		}
	}
	return fields
}
