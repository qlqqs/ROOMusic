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

func generateCorpus(ctx context.Context, corpus []corpusEntry, out, ffmpeg, reference string) error {
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
	opts := musicbrainz.Options{}
	mbURL := &url.URL{Scheme: "https", Host: "musicbrainz.org"}
	proxy, err := http.ProxyFromEnvironment(&http.Request{URL: mbURL})
	if err != nil {
		return err
	}
	if proxy != nil {
		// bench 只读取清单中固定 MusicBrainz 主机的公开发行，不跟随重定向。
		opts.NewClient = func(o egress.Options) (*http.Client, error) {
			return &http.Client{Timeout: o.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			}}, nil
		}
	}
	client := musicbrainz.New(opts)
	var releases []sources.Release
	var names []string
	totalTracks := 0
	for i, entry := range corpus {
		name := fmt.Sprintf("%02d - %s", i+1, fileLabel(entry.Artist+" - "+entry.Title))
		final := filepath.Join(out, name)
		if _, err := os.Lstat(final); !errors.Is(err, os.ErrNotExist) {
			if err != nil {
				return err
			}
			return fmt.Errorf("曲库输出已存在：%s", final)
		}
		release, err := client.ReleaseByID(ctx, entry.ID)
		if err != nil {
			return fmt.Errorf("读取 %s (%s)：%w", entry.Title, entry.ID, err)
		}
		dir := filepath.Join(stage, name)
		if err := os.Mkdir(dir, 0755); err != nil {
			return err
		}
		count, err := writeAlbum(ctx, binary, dir, entry, release)
		if err != nil {
			return fmt.Errorf("生成 %s (%s)：%w", entry.Title, entry.ID, err)
		}
		totalTracks += count
		names = append(names, name)
		if reference != "" {
			releases = append(releases, release)
		}
	}
	if reference != "" {
		data, err := json.MarshalIndent(releases, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(reference, append(data, '\n'), 0644); err != nil {
			return err
		}
	}
	// 全部音频编码成功才将专辑移到曲库中，失败的编码和远端读取可直接重试。
	for _, name := range names {
		if err := os.Rename(filepath.Join(stage, name), filepath.Join(out, name)); err != nil {
			return err
		}
	}
	return writeResult(map[string]any{"directory": out, "releases": len(names), "tracks": totalTracks})
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

func writeAlbum(ctx context.Context, binary, dir string, entry corpusEntry, r sources.Release) (int, error) {
	var count int
	for _, m := range r.Media {
		if len(m.Tracks) != m.TrackCount {
			return 0, fmt.Errorf("第 %d 碟曲目表不完整", m.Position)
		}
		for _, t := range m.Tracks {
			if t.LengthMS == nil || *t.LengthMS <= 0 {
				return 0, fmt.Errorf("第 %d 碟第 %d 首缺少版本时长", m.Position, t.Position)
			}
			tags := trackTags(entry, r, m, t)
			title := t.Title
			if title == "" {
				title = t.RecordingTitle
			}
			filename := fmt.Sprintf("%02d-%02d - %s.flac", m.Position, t.Position, fileLabel(title))
			// 每毫秒恰好 16 个采样，atrim 可在帧内按采样数截断最后一帧。
			args := []string{"-nostdin", "-v", "error", "-f", "lavfi", "-i", "anullsrc=r=16000:cl=mono",
				"-af", fmt.Sprintf("atrim=end_sample=%d", *t.LengthMS*16), "-c:a", "flac", "-map_metadata", "-1", "-fflags", "+bitexact", "-flags:a", "+bitexact"}
			for _, tag := range tags {
				args = append(args, "-metadata", tag)
			}
			args = append(args, filepath.Join(dir, filename))
			if stderr, err := exec.CommandContext(ctx, binary, args...).CombinedOutput(); err != nil {
				return 0, fmt.Errorf("%s：%w %s", filename, err, stderr)
			}
			count++
		}
	}
	if count == 0 {
		return 0, errors.New("发行没有可生成的曲目")
	}
	return count, nil
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

func trackTags(entry corpusEntry, r sources.Release, m sources.Medium, t sources.Track) []string {
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
	var tags []string
	for k, v := range fields {
		if v != "" {
			tags = append(tags, k+"="+v)
		}
	}
	slices.Sort(tags)
	return tags
}
