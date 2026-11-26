package curation

import (
	"context"
	"database/sql"
	"encoding/json"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/operations"
)

type localField struct {
	Value        string                `json:"value"`
	Authenticity metadata.Authenticity `json:"authenticity"`
	Requested    bool                  `json:"requested"`
}

type localTrack struct {
	ID     string            `json:"track_id"`
	File   string            `json:"file"`
	Tags   map[string]string `json:"tags,omitempty"`
	Title  localField        `json:"title"`
	Artist localField        `json:"artist"`
}

type localDirectory struct {
	Path   string       `json:"path"`
	Tracks []localTrack `json:"tracks"`
}

type localAlbum struct {
	DirectoryKey string            `json:"directory_key"`
	Tags         map[string]string `json:"tags,omitempty"`
	Title        localField        `json:"title"`
	Artist       localField        `json:"artist"`
	Directories  []localDirectory  `json:"directories"`
}

var localNoise = regexp.MustCompile(`(?i)\[[^\]]+\]|【[^】]+】|(^|[^\pL\pN])((19|20)[0-9]{2}|flac|wav|mp3|ape|alac|dsd[0-9]*|[0-9]+[ -]?(bit|khz)|cd[ ._-]*[0-9]+|disc[ ._-]*[0-9]+|[a-z]{2,6}-[0-9]{2,}|(?-i:[A-Z]{2,6})[0-9]{3,8})([^\pL\pN]|$)|(^|[^0-9])(19|20)[0-9]{2}年|^[0-9]{1,3}[ ._-]+\S`)
var localGeneric = regexp.MustCompile(`(?i)^(?:cd|disc|track|disk|音轨|碟)?[ ._0-9-]*$`)

func numericLocalValue(value string) bool {
	digit := false
	for _, r := range value {
		switch {
		case unicode.IsDigit(r):
			digit = true
		case unicode.IsSpace(r), unicode.IsPunct(r):
		default:
			return false
		}
	}
	return digit
}

func cleanupField(value string, auth metadata.Authenticity, protected bool) localField {
	return localField{value, auth, !protected && (auth == metadata.Placeholder || auth == metadata.Inferred && (localNoise.MatchString(value) || numericLocalValue(value)))}
}

func localAlbumInput(e catalog.CurationEvidence) (localAlbum, bool) {
	artist := e.AlbumArtist
	if artist == "" {
		artist = e.Artist
	}
	titleProtected := e.Corrected["title"] || e.Automatic["title"] && e.LocalTitle == ""
	a := localAlbum{
		Title:  cleanupField(e.Title, e.Authenticity["title"], titleProtected),
		Artist: cleanupField(artist, e.SelectedArtistAuth(), e.Corrected["album_artist"] || e.Automatic["album_artist"] && e.LocalArtist == "" || e.Compilation),
	}
	compoundTitle := a.Title.Authenticity == metadata.Inferred && a.Artist.Authenticity == metadata.Inferred && strings.Contains(e.Title, " - ")
	if compoundTitle && !titleProtected {
		a.Title.Requested = true
	}
	a.Tags = map[string]string{}
	if a.Title.Authenticity == metadata.Real && !e.Corrected["title"] {
		a.Tags["title"] = e.Title
	}
	if a.Artist.Authenticity == metadata.Real && !e.Corrected["album_artist"] {
		a.Tags["artist"] = artist
	}
	single := e.CandidateKind == "loose_unknown" && len(e.Tracks) == 1
	needed := a.Title.Requested || a.Artist.Requested
	// 即使曲目文件名已解析，目录留下的数字字段和复合专辑名仍需整理。
	albumNoise := a.Title.Requested && (compoundTitle || numericLocalValue(e.Title)) || a.Artist.Requested && numericLocalValue(artist)
	parsed := len(e.Tracks) > 0 && !albumNoise
	type directory struct{ root, name string }
	index := make(map[directory]int)
	for _, t := range e.Tracks {
		track := localTrack{
			ID: t.ID, File: path.Base(t.RelativePath), Tags: t.LocalTags,
			Title:  cleanupField(t.Title, t.Authenticity["title"], t.TitleCorrected || t.TitleAutomatic && t.LocalTitle == ""),
			Artist: cleanupField(t.Artist, t.Authenticity["artist"], !single || t.ArtistCorrected || t.ArtistAutomatic && t.LocalArtist == ""),
		}
		needed = needed || track.Title.Requested || track.Artist.Requested
		parsed = parsed && t.TitleObservation.Source == "filename_fallback" && t.ArtistObservation.Source == "filename_fallback" && !track.Title.Requested && !localNoise.MatchString(t.Artist) && !(track.Artist.Requested && numericLocalValue(t.Artist))
		dir := directory{t.SourceRootID, path.Dir(t.RelativePath)}
		i, found := index[dir]
		if !found {
			i = len(a.Directories)
			index[dir] = i
			a.Directories = append(a.Directories, localDirectory{Path: dir.name})
			if i == 0 {
				a.DirectoryKey = operations.Fingerprint([]string{dir.root, dir.name})
			}
		}
		a.Directories[i].Tracks = append(a.Directories[i].Tracks, track)
	}
	if !needed || parsed {
		return a, false
	}
	for _, dir := range a.Directories {
		for _, component := range strings.Split(dir.Path, "/") {
			if informativeLocalText(component) {
				return a, true
			}
		}
		for _, t := range dir.Tracks {
			if informativeLocalText(strings.TrimSuffix(t.File, path.Ext(t.File))) {
				return a, true
			}
			for _, field := range []string{"title", "artist", "album", "album_artist"} {
				if informativeLocalText(t.Tags[field]) {
					return a, true
				}
			}
		}
	}
	return a, false
}

func informativeLocalText(value string) bool {
	return strings.IndexFunc(value, unicode.IsLetter) >= 0 && !localGeneric.MatchString(value)
}

// ProjectLocalItems 复用程序的分页盘点；同一份扫描输入只提交给模型一次。
func ProjectLocalItems(ctx context.Context, tx *sql.Tx, ids []string, entries map[string]catalog.CurationEvidence) error {
	type record struct {
		ID       string     `json:"id"`
		Release  string     `json:"release_id"`
		Digest   string     `json:"digest"`
		Evidence localAlbum `json:"evidence"`
	}
	inputs := []record{}
	for _, id := range ids {
		e, visible := entries[id]
		if !visible {
			continue
		}
		a, ok := localAlbumInput(e)
		if !ok {
			continue
		}
		digest := operations.Fingerprint(a)
		inputs = append(inputs, record{DerivedKey(id, "local_cleanup:"+digest), id, digest, a})
	}
	raw, err := json.Marshal(inputs)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE curation_pending_items p SET status='resolved', resolution='inputs_changed',resolved_at=now(),updated_at=now()
 WHERE p.kind='local_cleanup' AND p.target_id=ANY($1::uuid[]) AND status<>'resolved'
 AND NOT EXISTS (SELECT FROM jsonb_to_recordset($2::jsonb) AS s(id uuid) WHERE s.id=p.id)`, ids, raw)
	if err != nil || len(inputs) == 0 {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO curation_pending_items(id,target_type,target_id,kind,status,rule_version,input_digest,identity_digest,evidence)
 SELECT s.id,'release',s.release_id,'local_cleanup','waiting_model',1,s.digest,s.digest,s.evidence FROM jsonb_to_recordset($1::jsonb)
 AS s(id uuid,release_id uuid,digest text,evidence jsonb)
 ON CONFLICT (id) DO UPDATE SET status='waiting_model',resolution=NULL,resolved_at=NULL,updated_at=now(),claimed_at=NULL,conversation_id=NULL,next_attempt_at=NULL,attempts=0
 WHERE curation_pending_items.resolution='inputs_changed'`, raw)
	return err
}

func (a localAlbum) fragments() []string {
	var out []string
	for _, dir := range a.Directories {
		out = append(out, strings.Split(dir.Path, "/")...)
		for _, t := range dir.Tracks {
			out = append(out, t.File)
			for _, v := range t.Tags {
				out = append(out, v)
			}
		}
	}
	for _, tag := range a.Tags {
		out = append(out, tag)
	}
	slices.Sort(out)
	return slices.Compact(out)
}
