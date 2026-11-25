package protocol

type JukeboxStatus struct {
	CurrentIndex int     `xml:"currentIndex,attr" json:"currentIndex"`
	Playing      bool    `xml:"playing,attr" json:"playing"`
	Gain         float64 `xml:"gain,attr" json:"gain"`
	Position     int     `xml:"position,attr" json:"position"`
}

type JukeboxPlaylist struct {
	JukeboxStatus
	Entries []Song `xml:"entry" json:"entry"`
}
