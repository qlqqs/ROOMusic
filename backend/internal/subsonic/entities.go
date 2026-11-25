// Package subsonic 将目录的有效值映射成客户端共用的协议实体。
package subsonic

import (
	"mime"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/subsonic/protocol"
)

// PersonalState 按当前账号与实体批量装入，以索引得到的单项覆盖目录响应。
type PersonalState struct {
	StarredAt *time.Time
	Rating    *int
}

func starred(state PersonalState) string {
	if state.StarredAt == nil {
		return ""
	}
	return state.StarredAt.UTC().Format(time.RFC3339Nano)
}

func artistRefs(refs []catalog.ArtistRef) []protocol.NamedArtist {
	items := make([]protocol.NamedArtist, 0, len(refs))
	for _, ref := range refs {
		items = append(items, protocol.NamedArtist{ID: ref.ArtistID, Name: ref.DisplayName})
	}
	return items
}

func albumArtists(r catalog.ReleaseSummary) (string, []protocol.NamedArtist) {
	if r.AlbumArtist != nil && len(r.AlbumArtistRefs) > 0 {
		return *r.AlbumArtist, artistRefs(r.AlbumArtistRefs)
	}
	name := r.Artist
	if r.AlbumArtist != nil {
		name = *r.AlbumArtist
	}
	return name, artistRefs(r.ArtistRefs)
}

func genreFields(genres []string) (string, []protocol.ItemGenre) {
	if len(genres) == 0 {
		return "", nil
	}
	items := make([]protocol.ItemGenre, len(genres))
	for i, name := range genres {
		items[i] = protocol.ItemGenre{Name: name}
	}
	return genres[0], items
}

func Song(t catalog.BrowseTrack, state PersonalState) protocol.Song {
	s := protocol.Song{ID: t.TrackID, Parent: t.GroupID, AlbumID: t.GroupID, Album: t.Release.Title, Title: t.Title,
		Artist: t.Artist, DisplayArtist: t.Artist, Artists: artistRefs(t.ArtistRefs), Track: t.Position, DiscNumber: t.Disc,
		Year: t.Release.Year, Size: t.Size, Suffix: t.Suffix, Type: "music", MediaType: "song", SortName: t.Title, MusicBrainzID: t.MusicBrainzID,
		BitRate: t.Source.Bitrate, BitDepth: t.Source.BitDepth, SamplingRate: t.Source.SampleRate, ChannelCount: t.Source.Channels, Starred: starred(state), UserRating: state.Rating}
	s.DisplayAlbumArtist, s.AlbumArtists = albumArtists(t.Release)
	if len(s.Artists) > 0 {
		s.ArtistID = s.Artists[0].ID
	}
	s.Genre, s.Genres = genreFields(t.Genres)
	if t.DurationSeconds != nil {
		s.Duration = int(*t.DurationSeconds)
	}
	if t.Release.Artwork != nil {
		s.CoverArt = "al-" + t.GroupID
	}
	if t.Suffix != "" {
		s.ContentType = audioMIME(t.Suffix)
	}
	if t.TrackGain != nil {
		s.ReplayGain = &protocol.ReplayGain{TrackGain: t.TrackGain, TrackPeak: t.TrackPeak}
	}
	return s
}

func audioMIME(suffix string) string {
	switch suffix {
	case "mp3":
		return "audio/mpeg"
	case "m4a", "mp4":
		return "audio/mp4"
	case "flac":
		return "audio/flac"
	case "wav", "wave":
		return "audio/wav"
	case "oga", "ogg", "opus":
		return "audio/ogg"
	case "aif", "aiff":
		return "audio/aiff"
	case "ape":
		return "audio/ape"
	case "wv":
		return "audio/wavpack"
	case "dsf":
		return "audio/dsf"
	case "dff":
		return "audio/dff"
	}
	return mime.TypeByExtension("." + suffix)
}

func Album(a catalog.BrowseAlbum, state PersonalState) protocol.Album {
	r := a.Representative
	p := protocol.Album{ID: a.ID, Name: r.Title, SortName: r.Title, Year: r.Year, SongCount: r.TrackCount, Duration: int(a.Duration),
		Created: a.AddedAt.UTC().Format(time.RFC3339Nano), MusicBrainzID: a.MusicBrainzID, Starred: starred(state), UserRating: state.Rating}
	p.Artist, p.Artists = albumArtists(r)
	p.DisplayArtist = p.Artist
	if len(p.Artists) > 0 {
		p.ArtistID = p.Artists[0].ID
	}
	p.Genre, p.Genres = genreFields(a.Genres)
	if r.Artwork != nil {
		p.CoverArt = "al-" + a.ID
	}
	return p
}

func Artist(a catalog.BrowseArtist, state PersonalState) protocol.Artist {
	p := protocol.Artist{ID: a.ID, Name: a.Name, SortName: a.SortName, AlbumCount: a.AlbumCount, MusicBrainzID: a.MusicBrainzID, Starred: starred(state), UserRating: state.Rating}
	if a.Image != nil || a.AlbumCount > 0 {
		p.CoverArt = "ar-" + a.ID
	}
	return p
}
