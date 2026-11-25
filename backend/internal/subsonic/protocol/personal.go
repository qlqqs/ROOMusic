package protocol

type Playlists struct {
	Items []Playlist `xml:"playlist" json:"playlist"`
}

type Playlist struct {
	ID        string `xml:"id,attr" json:"id"`
	Name      string `xml:"name,attr" json:"name"`
	Owner     string `xml:"owner,attr" json:"owner"`
	Public    bool   `xml:"public,attr" json:"public"`
	SongCount int64  `xml:"songCount,attr" json:"songCount"`
	Duration  int    `xml:"duration,attr" json:"duration"`
	Created   string `xml:"created,attr" json:"created"`
	Changed   string `xml:"changed,attr" json:"changed"`
	CoverArt  string `xml:"coverArt,attr" json:"coverArt"`
	Entries   []Song `xml:"entry" json:"entry,omitempty"`
}

type Lyrics struct {
	Artist string `xml:"artist,attr,omitempty" json:"artist,omitempty"`
	Title  string `xml:"title,attr,omitempty" json:"title,omitempty"`
	Value  string `xml:",chardata" json:"value"`
}

type LyricsList struct {
	Items []StructuredLyrics `xml:"structuredLyrics" json:"structuredLyrics"`
}

type StructuredLyrics struct {
	Language string       `xml:"lang,attr" json:"lang"`
	Synced   bool         `xml:"synced,attr" json:"synced"`
	Lines    []LyricsLine `xml:"line" json:"line"`
}

type LyricsLine struct {
	Start *int64 `xml:"start,attr,omitempty" json:"start,omitempty"`
	Value string `xml:",chardata" json:"value"`
}
