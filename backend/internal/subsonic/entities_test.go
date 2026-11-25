package subsonic_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/subsonic"
	"github.com/qlqq/roomusic/backend/internal/subsonic/protocol"
)

func TestSubsonicBrowseEntityMapping(t *testing.T) {
	year, duration, size := 1998, 120.75, int64(4096)
	bitrate, depth, rate, channels := 800, 24, 96000, 2
	gain, peak := -3.5, 0.87
	albumArtist := "Atlas Ensemble"
	stamp := time.Date(2026, 11, 25, 12, 30, 0, 125000000, time.FixedZone("fixture", 8*3600))
	const utc = "2026-11-25T04:30:00.125Z"
	state := subsonic.PersonalState{StarredAt: &stamp}
	release := catalog.ReleaseSummary{
		ID: "version", Title: "Double Album", Artist: "Original Credit", AlbumArtist: &albumArtist,
		Year: &year, TrackCount: 9, Artwork: &catalog.ReleaseArtwork{ResourceID: "internal-key"},
		ArtistRefs:      []catalog.ArtistRef{{ArtistID: "original", DisplayName: "Original Credit"}},
		AlbumArtistRefs: []catalog.ArtistRef{{ArtistID: "ensemble", Name: "Atlas Alias", DisplayName: "Atlas Ensemble"}},
	}
	track := catalog.BrowseTrack{
		TrackSummary: catalog.TrackSummary{
			TrackID: "track", GroupID: "group", Title: "Finale", Artist: "Soloist / Guest", Release: release,
			ArtistRefs: []catalog.ArtistRef{
				{ArtistID: "soloist", Name: "Soloist Alias", DisplayName: "Soloist"},
				{ArtistID: "guest", Name: "Guest", DisplayName: "Guest"},
			},
			DurationSeconds: &duration,
			Source: catalog.TrackAudioFacts{SourceKind: "cue_virtual", Bitrate: &bitrate,
				BitDepth: &depth, SampleRate: &rate, Channels: &channels},
		},
		Position: 3, Disc: 2, Suffix: "flac", Size: &size, Genres: []string{"Rock", "Jazz"},
		MusicBrainzID: "recording", TrackGain: &gain, TrackPeak: &peak,
	}
	genres := []protocol.ItemGenre{{Name: "Rock"}, {Name: "Jazz"}}
	albumArtists := []protocol.NamedArtist{{ID: "ensemble", Name: "Atlas Ensemble"}}
	t.Run("Song", func(t *testing.T) {
		want := protocol.Song{
			ID: "track", Parent: "group", AlbumID: "group", Album: "Double Album", Title: "Finale",
			Artist: "Soloist / Guest", ArtistID: "soloist", DisplayArtist: "Soloist / Guest",
			Artists:            []protocol.NamedArtist{{ID: "soloist", Name: "Soloist"}, {ID: "guest", Name: "Guest"}},
			DisplayAlbumArtist: albumArtist, AlbumArtists: albumArtists, Track: 3, DiscNumber: 2,
			Year: &year, Genre: "Rock", Genres: genres, CoverArt: "al-group", Size: &size,
			Suffix: "flac", ContentType: "audio/flac", Duration: 120, BitRate: &bitrate, Type: "music",
			MediaType: "song", MusicBrainzID: "recording", SortName: "Finale", BitDepth: &depth,
			SamplingRate: &rate, ChannelCount: &channels, Starred: utc,
			ReplayGain: &protocol.ReplayGain{TrackGain: &gain, TrackPeak: &peak},
		}
		if got := subsonic.Song(track, state); !reflect.DeepEqual(got, want) {
			t.Errorf("曲目映射 = %+v，期望 %+v", got, want)
		}
	})
	t.Run("Album", func(t *testing.T) {
		want := protocol.Album{
			ID: "group", Name: "Double Album", SortName: "Double Album", Artist: albumArtist, ArtistID: "ensemble",
			DisplayArtist: albumArtist, Artists: albumArtists, CoverArt: "al-group", Year: &year,
			SongCount: 9, Duration: 120, Created: utc, Starred: utc, Genre: "Rock", Genres: genres, MusicBrainzID: "release",
		}
		got := subsonic.Album(catalog.BrowseAlbum{
			ID: "group", Representative: release, Genres: track.Genres, AddedAt: stamp, Duration: duration, MusicBrainzID: "release",
		}, state)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("专辑映射 = %+v，期望 %+v", got, want)
		}
	})
	t.Run("Artist", func(t *testing.T) {
		artist := catalog.BrowseArtist{
			ArtistSummary: catalog.ArtistSummary{ID: "ensemble", Name: albumArtist, AlbumCount: 2},
			SortName:      "atlas ensemble", MusicBrainzID: "artist-mbid",
		}
		want := protocol.Artist{
			ID: "ensemble", Name: albumArtist, SortName: "atlas ensemble", MusicBrainzID: "artist-mbid",
			AlbumCount: 2, CoverArt: "ar-ensemble", Starred: utc,
		}
		if got := subsonic.Artist(artist, state); !reflect.DeepEqual(got, want) {
			t.Errorf("艺人映射 = %+v，期望 %+v", got, want)
		}
		artist.AlbumCount = 0
		if got := subsonic.Artist(artist, subsonic.PersonalState{}); got.CoverArt != "" || got.Starred != "" {
			t.Errorf("无图片、无专辑、无收藏的艺人带有可选字段：%+v", got)
		}
		artist.Image = &release.Artwork.ResourceID
		if got := subsonic.Artist(artist, subsonic.PersonalState{}); got.CoverArt != "ar-ensemble" {
			t.Errorf("只有艺人图片时封面 ID = %q，期望 ar-ensemble", got.CoverArt)
		}
	})
	t.Run("MissingOptionals", func(t *testing.T) {
		got := subsonic.Song(catalog.BrowseTrack{TrackSummary: catalog.TrackSummary{
			TrackID: "track", GroupID: "group", Title: "Bare Track",
			Release: catalog.ReleaseSummary{Title: "Bare Album", Artist: "Release Credit"},
		}}, subsonic.PersonalState{})
		if got.DisplayAlbumArtist != "Release Credit" || got.CoverArt != "" || got.Genre != "" || got.Genres != nil ||
			got.ReplayGain != nil || got.Starred != "" || got.ArtistID != "" || got.Duration != 0 ||
			got.ContentType != "" || got.Size != nil || got.Year != nil || len(got.Artists) != 0 || len(got.AlbumArtists) != 0 {
			t.Errorf("缺失元数据时的映射不符：%+v", got)
		}
	})
}
