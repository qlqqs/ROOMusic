// Package protocol 定义 Subsonic 请求参数、响应外壳和双格式实体。
package protocol

import (
	"encoding/json"
	"encoding/xml"
	"net/http"
)

const (
	General            = 0
	MissingParameter   = 10
	ClientTooOld       = 20
	ServerTooOld       = 30
	InvalidCredentials = 40
	TokenUnsupported   = 41
	Forbidden          = 50
	NotFound           = 70
)

type Error struct {
	Code    int    `xml:"code,attr" json:"code"`
	Message string `xml:"message,attr" json:"message"`
}

type License struct {
	Valid bool `xml:"valid,attr" json:"valid"`
}

type Extension struct {
	Name     string `xml:"name,attr" json:"name"`
	Versions []int  `xml:"versions" json:"versions"`
}

type MusicFolders struct {
	Folders []MusicFolder `xml:"musicFolder" json:"musicFolder"`
}
type MusicFolder struct {
	ID   int    `xml:"id,attr" json:"id"`
	Name string `xml:"name,attr" json:"name"`
}

type User struct {
	Username            string `xml:"username,attr" json:"username"`
	ScrobblingEnabled   bool   `xml:"scrobblingEnabled,attr" json:"scrobblingEnabled"`
	AdminRole           bool   `xml:"adminRole,attr" json:"adminRole"`
	SettingsRole        bool   `xml:"settingsRole,attr" json:"settingsRole"`
	DownloadRole        bool   `xml:"downloadRole,attr" json:"downloadRole"`
	UploadRole          bool   `xml:"uploadRole,attr" json:"uploadRole"`
	PlaylistRole        bool   `xml:"playlistRole,attr" json:"playlistRole"`
	CoverArtRole        bool   `xml:"coverArtRole,attr" json:"coverArtRole"`
	CommentRole         bool   `xml:"commentRole,attr" json:"commentRole"`
	PodcastRole         bool   `xml:"podcastRole,attr" json:"podcastRole"`
	StreamRole          bool   `xml:"streamRole,attr" json:"streamRole"`
	JukeboxRole         bool   `xml:"jukeboxRole,attr" json:"jukeboxRole"`
	ShareRole           bool   `xml:"shareRole,attr" json:"shareRole"`
	VideoConversionRole bool   `xml:"videoConversionRole,attr" json:"videoConversionRole"`
	Folders             []int  `xml:"folder" json:"folder"`
}

type NowPlaying struct {
	Entries []NowPlayingEntry `xml:"entry" json:"entry"`
}
type NowPlayingEntry struct {
	Song
	Username   string `xml:"username,attr" json:"username"`
	MinutesAgo int    `xml:"minutesAgo,attr" json:"minutesAgo"`
	PlayerName string `xml:"playerName,attr" json:"playerName"`
}
type Response struct {
	NowPlaying    *NowPlaying   `xml:"nowPlaying,omitempty" json:"nowPlaying,omitempty"`
	XMLName       xml.Name      `xml:"http://subsonic.org/restapi subsonic-response" json:"-"`
	Status        string        `xml:"status,attr" json:"status"`
	Version       string        `xml:"version,attr" json:"version"`
	Type          string        `xml:"type,attr" json:"type"`
	ServerVersion string        `xml:"serverVersion,attr" json:"serverVersion"`
	OpenSubsonic  bool          `xml:"openSubsonic,attr" json:"openSubsonic"`
	Error         *Error        `xml:"error,omitempty" json:"error,omitempty"`
	License       *License      `xml:"license,omitempty" json:"license,omitempty"`
	Extensions    []Extension   `xml:"openSubsonicExtensions,omitempty" json:"openSubsonicExtensions,omitempty"`
	MusicFolders  *MusicFolders `xml:"musicFolders,omitempty" json:"musicFolders,omitempty"`
	User          *User         `xml:"user,omitempty" json:"user,omitempty"`
	Artists       *Artists      `xml:"artists,omitempty" json:"artists,omitempty"`
	Artist        *Artist       `xml:"artist,omitempty" json:"artist,omitempty"`
	Album         *Album        `xml:"album,omitempty" json:"album,omitempty"`
	Song          *Song         `xml:"song,omitempty" json:"song,omitempty"`
	AlbumList2    *AlbumList    `xml:"albumList2,omitempty" json:"albumList2,omitempty"`
	SearchResult3 *SearchResult `xml:"searchResult3,omitempty" json:"searchResult3,omitempty"`
	RandomSongs   *RandomSongs  `xml:"randomSongs,omitempty" json:"randomSongs,omitempty"`
	Genres        *Genres       `xml:"genres,omitempty" json:"genres,omitempty"`
	ArtistInfo2   *ArtistInfo   `xml:"artistInfo2,omitempty" json:"artistInfo2,omitempty"`
	AlbumInfo     *AlbumInfo    `xml:"albumInfo,omitempty" json:"albumInfo,omitempty"`
	Starred2      *SearchResult `xml:"starred2,omitempty" json:"starred2,omitempty"`
	Playlists     *Playlists    `xml:"playlists,omitempty" json:"playlists,omitempty"`
	Playlist      *Playlist     `xml:"playlist,omitempty" json:"playlist,omitempty"`
	Lyrics        *Lyrics       `xml:"lyrics,omitempty" json:"lyrics,omitempty"`
	LyricsList    *LyricsList   `xml:"lyricsList,omitempty" json:"lyricsList,omitempty"`

	JukeboxStatus   *JukeboxStatus   `xml:"jukeboxStatus,omitempty" json:"jukeboxStatus,omitempty"`
	JukeboxPlaylist *JukeboxPlaylist `xml:"jukeboxPlaylist,omitempty" json:"jukeboxPlaylist,omitempty"`
}

func Failure(code int, message string) Response {
	return Response{Error: &Error{Code: code, Message: message}}
}

func Write(w http.ResponseWriter, format, serverVersion string, httpStatus int, response Response) {
	response.Status = "ok"
	if response.Error != nil {
		response.Status = "failed"
	}
	response.Version = "1.16.1"
	response.Type = "roomusic"
	response.OpenSubsonic = true
	response.ServerVersion = serverVersion
	w.Header().Set("Cache-Control", "no-store")
	if format == "json" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(httpStatus)
		_ = json.NewEncoder(w).Encode(struct {
			Response Response `json:"subsonic-response"`
		}{response})
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(httpStatus)
	_ = xml.NewEncoder(w).Encode(response)
}
