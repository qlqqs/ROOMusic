package app

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/library/parser/parsertest"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/musicbrainz"
)

type metricSample struct {
	ID, Shape        string
	Negative         bool
	ProgramDecidable bool `json:"program_decidable"`
	Local            metricLocal
	External         metricExternal
	Expect           metricExpect
}

type metricExpect struct {
	Scan                *metricScanExpect
	Accept              []string
	Suggestions         map[string]string
	SuggestionCanAccept *bool `json:"suggestion_can_accept"`

	Release, Group  string
	GroupMatch      string `json:"group_match"`
	Recordings      []string
	Fields          map[string]string
	TrackFields     []map[string]string `json:"track_fields"`
	Artists         map[string]string
	GroupWith       []string `json:"group_with"`
	MergeArtistWith []string `json:"merge_artist_with"`
}

func (e metricExpect) withOutcomeRules() metricExpect {
	if len(e.Accept) == 0 {
		switch {
		case e.GroupMatch != "":
			e.Accept = []string{"release_group"}
		case e.Release != "" || slices.ContainsFunc(e.Recordings, func(id string) bool { return id != "" }):
			e.Accept = []string{"release"}
		default:
			e.Accept = []string{"no_usable_candidates", "unlisted"}
		}
	}
	// 允许放弃对应时保留建议终态。仅反例一律要求建议不可采纳。
	if slices.Contains(e.Accept, "unlisted") || slices.Contains(e.Accept, "no_usable_candidates") {
		if !slices.Contains(e.Accept, "suggestion") {
			e.Accept = append(slices.Clone(e.Accept), "suggestion")
		}
		if !e.allowsMatch() {
			canAccept := false
			e.SuggestionCanAccept = &canAccept
		}
	}
	return e
}

func (e metricExpect) allowsMatch() bool {
	return slices.Contains(e.Accept, "release") || slices.Contains(e.Accept, "release_group")
}

type metricLocal struct {
	Directory, Format, Album, Artist string
	LegacyAlbumHex                   string `json:"legacy_album_hex"`
	Tags                             map[string]string
	Cue                              bool
	Tracks                           []metricLocalTrack
}

type metricLocalTrack struct {
	File, Title, Artist string
	Seconds             int
	Tags                map[string]string
}

type metricExternal struct {
	Artists  []metricArtist
	Releases []metricRelease
}

type metricArtist struct {
	ID, Name string
	Aliases  []string
}

type metricCredit struct{ Artist, JoinPhrase string }

type metricRelease struct {
	Credits                                                                             []metricCredit
	ID, Title, Group, GroupTitle, Artist, Date, Country, Label, Catalog, Barcode, Image string
	Tracks                                                                              []metricRecording
}

type metricRecording struct {
	Disc                             int
	Credits                          []metricCredit
	ID, TrackID, Title, Artist, ISRC string
	Length                           int
}

func loadMetricSamples(t *testing.T) []metricSample {
	t.Helper()
	data, err := os.ReadFile("testdata/curation_regression/samples.json")
	requireCurationOK(t, err)
	var samples []metricSample
	requireCurationOK(t, json.Unmarshal(data, &samples))
	if len(samples) == 0 {
		t.Fatal("曲库清单为空")
	}
	return samples
}

func (s metricSample) fileTags(track metricLocalTrack) []string {
	tags := map[string]string{"ALBUM": s.Local.Album, "ARTIST": s.Local.Artist, "TITLE": track.Title}
	maps.Copy(tags, s.Local.Tags)
	if track.Artist != "" {
		tags["ARTIST"] = track.Artist
	}
	maps.Copy(tags, track.Tags)
	comments := make([]string, 0, len(tags))
	for _, key := range slices.Sorted(maps.Keys(tags)) {
		if tags[key] != "" {
			comments = append(comments, key+"="+tags[key])
		}
	}
	return comments
}

func writeMetricSample(t *testing.T, root string, s metricSample) {
	t.Helper()
	dir := filepath.Join(root, s.Local.Directory)
	requireCurationOK(t, os.MkdirAll(dir, 0755))
	if s.Local.Cue {
		writeMetricCue(t, root, s)
		return
	}
	for _, track := range s.Local.Tracks {
		tags := s.fileTags(track)
		path := filepath.Join(dir, track.File)
		requireCurationOK(t, os.MkdirAll(filepath.Dir(path), 0755))
		if s.Local.Format == "flac" {
			// 扫描器从 STREAMINFO 读取总样本数和身份。每个虚构音轨具有稳定且独立的内容指纹。
			hash := sha256.Sum256([]byte(s.ID + "/" + track.File))
			data := parsertest.FLACWithAudio(t, [16]byte(hash[:16]), hash[:], tags...)
			stream := data[8:42]
			facts := binary.BigEndian.Uint64(stream[10:18])
			binary.BigEndian.PutUint64(stream[10:18], facts&^((1<<36)-1)|uint64(track.Seconds*parsertest.FLACSampleRate))
			requireCurationOK(t, os.WriteFile(path, data, 0644))
			continue
		}
		args := []string{"-nostdin", "-loglevel", "error", "-f", "lavfi", "-i", "anullsrc=r=44100:cl=mono", "-t", strconv.Itoa(track.Seconds)}
		if s.Local.LegacyAlbumHex != "" {
			// 音频帧交给 ffmpeg 生成，再按清单指定的原始字节附上标签。
			args = append(args, "-id3v2_version", "0", "-write_id3v1", "0")
			tags = nil
		}
		for _, tag := range tags {
			key, value, _ := strings.Cut(tag, "=")
			switch key {
			case "TRACKNUMBER":
				key = "track"
			case "DISCNUMBER":
				key = "disc"
			case "ALBUMARTIST":
				key = "album_artist"
			}
			args = append(args, "-metadata", key+"="+value)
		}
		if out, err := exec.CommandContext(t.Context(), b3RequireTool(t, "ROOMUSIC_TEST_FFMPEG"), append(args, path)...).CombinedOutput(); err != nil {
			t.Fatalf("生成 %s %v %s", path, err, out)
		}
		if s.Local.LegacyAlbumHex != "" {
			audio, err := os.ReadFile(path)
			requireCurationOK(t, err)
			data := metricLegacyMP3(t, audio, s, track)
			requireCurationOK(t, os.WriteFile(path, data, 0644))
		}
	}
}

func metricLegacyMP3(t *testing.T, audio []byte, s metricSample, track metricLocalTrack) []byte {
	t.Helper()
	album, err := hex.DecodeString(s.Local.LegacyAlbumHex)
	requireCurationOK(t, err)
	if s.Local.Format == "id3v1" {
		tag := make([]byte, 128)
		copy(tag, "TAG")
		copy(tag[3:33], track.Title)
		copy(tag[33:63], s.Local.Artist)
		copy(tag[63:93], album)
		tag[126], tag[127] = 1, 255
		return append(audio, tag...)
	}
	var frames []byte
	for _, pair := range [][2]string{{"TIT2", track.Title}, {"TPE1", s.Local.Artist}, {"TALB", string(album)}, {"TRCK", "1"}} {
		// ID3v2.3 文本帧首字节为 0，表示播放器会按 Latin-1 解释后续字节。
		header := make([]byte, 10)
		copy(header, pair[0])
		binary.BigEndian.PutUint32(header[4:8], uint32(1+len(pair[1])))
		frames = append(frames, header...)
		frames = append(frames, 0)
		frames = append(frames, pair[1]...)
	}
	return append(parsertest.ID3v23(frames), audio...)
}

func writeMetricCue(t *testing.T, root string, s metricSample) {
	t.Helper()
	var cue strings.Builder
	fmt.Fprintf(&cue, "PERFORMER %q\nTITLE %q\nFILE \"disc-image.flac\" WAVE\n", s.Local.Artist, s.Local.Album)
	seconds := 0
	for i, track := range s.Local.Tracks {
		fmt.Fprintf(&cue, "  TRACK %02d AUDIO\n    TITLE %q\n    PERFORMER %q\n    INDEX 01 %02d:%02d:00\n", i+1, track.Title, s.Local.Artist, seconds/60, seconds%60)
		seconds += track.Seconds
	}
	tone := fmt.Sprintf("anullsrc=r=44100:cl=stereo:d=%d", seconds)
	writeAcceptanceAudio(t, root, s.Local.Directory, "disc-image", tone, "ALBUM="+s.Local.Album, "ARTIST="+s.Local.Artist, "ALBUMARTIST="+s.Local.Artist)
	requireCurationOK(t, os.WriteFile(filepath.Join(root, s.Local.Directory, "disc.cue"), []byte(cue.String()), 0644))
}

func writeMetricCatalog(t *testing.T, samples []metricSample) string {
	t.Helper()
	type row = map[string]any
	var catalog struct {
		Artists, Groups, Releases, Recordings []row
	}
	artists, groups, releases, recordings := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, s := range samples {
		for _, a := range s.External.Artists {
			if artists[a.ID] {
				continue
			}
			artists[a.ID] = true
			var aliases []row
			for _, name := range a.Aliases {
				aliases = append(aliases, row{"name": name, "locale": "en", "primary": true})
			}
			catalog.Artists = append(catalog.Artists, row{"id": a.ID, "name": a.Name, "sort-name": a.Name, "aliases": aliases})
		}
		for _, r := range s.External.Releases {
			if releases[r.ID] {
				continue
			}
			releases[r.ID] = true
			if !groups[r.Group] {
				groups[r.Group] = true
				catalog.Groups = append(catalog.Groups, row{"id": r.Group, "title": r.GroupTitle, "primary-type": "Album", "first-release-date": r.Date})
			}
			var media []row
			toc := sources.DiscTOC{Leadout: 150}
			for _, track := range r.Tracks {
				if !recordings[track.ID] {
					recordings[track.ID] = true
					catalog.Recordings = append(catalog.Recordings, row{"id": track.ID, "title": track.Title, "artist": track.Artist, "length": track.Length, "isrc": track.ISRC, "credits": track.Credits})
				}
				disc := max(1, track.Disc)
				if disc == len(media)+1 {
					media = append(media, row{"tracks": []row{}})
				}
				if disc != len(media) {
					t.Fatalf("%s 的外部碟号不连续：%d", s.ID, disc)
				}
				medium := media[disc-1]
				medium["tracks"] = append(medium["tracks"].([]row), row{"id": track.TrackID, "recording": track.ID})
				toc.Offsets = append(toc.Offsets, toc.Leadout)
				toc.Leadout += track.Length * 75 / 1000
			}
			if s.Local.Cue {
				id, err := musicbrainz.DiscID(toc)
				requireCurationOK(t, err)
				media[0]["Disc"] = id
			}
			catalog.Releases = append(catalog.Releases, row{"id": r.ID, "title": r.Title, "artist": r.Artist, "group": r.Group, "media": media, "credits": r.Credits, "country": r.Country, "date": r.Date, "label": r.Label, "catalog": r.Catalog, "barcode": r.Barcode, "image": r.Image})
		}
	}
	dir := t.TempDir()
	requireCurationOK(t, os.WriteFile(filepath.Join(dir, "cover.png"), externalFixturePNG(t, 32, 32), 0644))
	data, err := json.Marshal(catalog)
	requireCurationOK(t, err)
	requireCurationOK(t, os.WriteFile(filepath.Join(dir, "catalog.json"), data, 0644))
	return dir
}
