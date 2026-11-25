package protocol

type NamedArtist struct {
	ID   string `xml:"id,attr" json:"id"`
	Name string `xml:"name,attr" json:"name"`
}

type ItemGenre struct {
	Name string `xml:"name,attr" json:"name"`
}

type DiscTitle struct {
	Disc  int    `xml:"disc,attr" json:"disc"`
	Title string `xml:"title,attr" json:"title"`
}

type ReplayGain struct {
	TrackGain *float64 `xml:"trackGain,attr,omitempty" json:"trackGain,omitempty"`
	TrackPeak *float64 `xml:"trackPeak,attr,omitempty" json:"trackPeak,omitempty"`
}

type Song struct {
	ID                 string        `xml:"id,attr" json:"id"`
	Parent             string        `xml:"parent,attr" json:"parent"`
	IsDir              bool          `xml:"isDir,attr" json:"isDir"`
	Title              string        `xml:"title,attr" json:"title"`
	Album              string        `xml:"album,attr" json:"album"`
	Artist             string        `xml:"artist,attr" json:"artist"`
	Track              int           `xml:"track,attr,omitempty" json:"track,omitempty"`
	Year               *int          `xml:"year,attr,omitempty" json:"year,omitempty"`
	Genre              string        `xml:"genre,attr,omitempty" json:"genre,omitempty"`
	CoverArt           string        `xml:"coverArt,attr,omitempty" json:"coverArt,omitempty"`
	Size               *int64        `xml:"size,attr,omitempty" json:"size,omitempty"`
	ContentType        string        `xml:"contentType,attr,omitempty" json:"contentType,omitempty"`
	Suffix             string        `xml:"suffix,attr,omitempty" json:"suffix,omitempty"`
	Duration           int           `xml:"duration,attr" json:"duration"`
	BitRate            *int          `xml:"bitRate,attr,omitempty" json:"bitRate,omitempty"`
	DiscNumber         int           `xml:"discNumber,attr" json:"discNumber"`
	AlbumID            string        `xml:"albumId,attr" json:"albumId"`
	ArtistID           string        `xml:"artistId,attr,omitempty" json:"artistId,omitempty"`
	IsVideo            bool          `xml:"isVideo,attr" json:"isVideo"`
	Type               string        `xml:"type,attr" json:"type"`
	MediaType          string        `xml:"mediaType,attr" json:"mediaType"`
	DisplayArtist      string        `xml:"displayArtist,attr" json:"displayArtist"`
	DisplayAlbumArtist string        `xml:"displayAlbumArtist,attr" json:"displayAlbumArtist"`
	MusicBrainzID      string        `xml:"musicBrainzId,attr,omitempty" json:"musicBrainzId,omitempty"`
	SortName           string        `xml:"sortName,attr,omitempty" json:"sortName,omitempty"`
	BitDepth           *int          `xml:"bitDepth,attr,omitempty" json:"bitDepth,omitempty"`
	SamplingRate       *int          `xml:"samplingRate,attr,omitempty" json:"samplingRate,omitempty"`
	ChannelCount       *int          `xml:"channelCount,attr,omitempty" json:"channelCount,omitempty"`
	UserRating         *int          `xml:"userRating,attr,omitempty" json:"userRating,omitempty"`
	Starred            string        `xml:"starred,attr,omitempty" json:"starred,omitempty"`
	Artists            []NamedArtist `xml:"artists,omitempty" json:"artists,omitempty"`
	AlbumArtists       []NamedArtist `xml:"albumArtists,omitempty" json:"albumArtists,omitempty"`
	Genres             []ItemGenre   `xml:"genres,omitempty" json:"genres,omitempty"`
	ReplayGain         *ReplayGain   `xml:"replayGain,omitempty" json:"replayGain,omitempty"`
}

type Album struct {
	ID            string        `xml:"id,attr" json:"id"`
	Name          string        `xml:"name,attr" json:"name"`
	Artist        string        `xml:"artist,attr" json:"artist"`
	ArtistID      string        `xml:"artistId,attr,omitempty" json:"artistId,omitempty"`
	CoverArt      string        `xml:"coverArt,attr,omitempty" json:"coverArt,omitempty"`
	SongCount     int64         `xml:"songCount,attr" json:"songCount"`
	Duration      int           `xml:"duration,attr" json:"duration"`
	Created       string        `xml:"created,attr" json:"created"`
	Year          *int          `xml:"year,attr,omitempty" json:"year,omitempty"`
	Genre         string        `xml:"genre,attr,omitempty" json:"genre,omitempty"`
	UserRating    *int          `xml:"userRating,attr,omitempty" json:"userRating,omitempty"`
	Starred       string        `xml:"starred,attr,omitempty" json:"starred,omitempty"`
	MusicBrainzID string        `xml:"musicBrainzId,attr,omitempty" json:"musicBrainzId,omitempty"`
	DisplayArtist string        `xml:"displayArtist,attr" json:"displayArtist"`
	SortName      string        `xml:"sortName,attr,omitempty" json:"sortName,omitempty"`
	Artists       []NamedArtist `xml:"artists,omitempty" json:"artists,omitempty"`
	Genres        []ItemGenre   `xml:"genres,omitempty" json:"genres,omitempty"`
	DiscTitles    []DiscTitle   `xml:"discTitles,omitempty" json:"discTitles,omitempty"`
	Songs         []Song        `xml:"song,omitempty" json:"song,omitempty"`
}

type Artist struct {
	ID            string  `xml:"id,attr" json:"id"`
	Name          string  `xml:"name,attr" json:"name"`
	AlbumCount    int64   `xml:"albumCount,attr" json:"albumCount"`
	CoverArt      string  `xml:"coverArt,attr,omitempty" json:"coverArt,omitempty"`
	UserRating    *int    `xml:"userRating,attr,omitempty" json:"userRating,omitempty"`
	Starred       string  `xml:"starred,attr,omitempty" json:"starred,omitempty"`
	MusicBrainzID string  `xml:"musicBrainzId,attr,omitempty" json:"musicBrainzId,omitempty"`
	SortName      string  `xml:"sortName,attr,omitempty" json:"sortName,omitempty"`
	Albums        []Album `xml:"album,omitempty" json:"album,omitempty"`
}

type ArtistIndex struct {
	Name    string   `xml:"name,attr" json:"name"`
	Artists []Artist `xml:"artist" json:"artist"`
}

type Artists struct {
	IgnoredArticles string        `xml:"ignoredArticles,attr" json:"ignoredArticles"`
	Indexes         []ArtistIndex `xml:"index" json:"index"`
}

type AlbumList struct {
	Albums []Album `xml:"album" json:"album"`
}

type RandomSongs struct {
	Songs []Song `xml:"song" json:"song"`
}

type SearchResult struct {
	Artists []Artist `xml:"artist" json:"artist"`
	Albums  []Album  `xml:"album" json:"album"`
	Songs   []Song   `xml:"song" json:"song"`
}

type Genre struct {
	Name       string `xml:",chardata" json:"value"`
	SongCount  int64  `xml:"songCount,attr" json:"songCount"`
	AlbumCount int64  `xml:"albumCount,attr" json:"albumCount"`
}

type Genres struct {
	Genres []Genre `xml:"genre" json:"genre"`
}

type ArtistInfo struct {
	Biography     string `xml:"biography,omitempty" json:"biography,omitempty"`
	MusicBrainzID string `xml:"musicBrainzId,omitempty" json:"musicBrainzId,omitempty"`
}

type AlbumInfo struct {
	MusicBrainzID string `xml:"musicBrainzId,omitempty" json:"musicBrainzId,omitempty"`
}
