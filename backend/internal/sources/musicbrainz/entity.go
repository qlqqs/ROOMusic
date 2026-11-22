package musicbrainz

// 只解码整理专辑需要的字段，支持实体读取与三种查找能力的投影。
type Entity struct {
	SortName    string   `json:"sort-name"`
	Type        string   `json:"type"`
	Status      string   `json:"status"`
	PrimaryType string   `json:"primary-type"`
	ISRCs       []string `json:"isrcs"`
	Aliases     []struct {
		Name    string `json:"name"`
		Locale  string `json:"locale"`
		Primary bool   `json:"primary"`
	} `json:"aliases"`
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	Title            string         `json:"title"`
	Disambiguation   string         `json:"disambiguation"`
	Date             string         `json:"date"`
	FirstReleaseDate string         `json:"first-release-date"`
	Country          string         `json:"country"`
	Barcode          string         `json:"barcode"`
	Length           *int64         `json:"length"`
	ArtistCredit     []ArtistCredit `json:"artist-credit"`
	LabelInfo        []struct {
		Label struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"label"`
		CatalogNumber string `json:"catalog-number"`
	} `json:"label-info"`
	Media []struct {
		TrackCount int `json:"track-count"`
		Discs      []struct {
			ID string `json:"id"`
		} `json:"discs"`
		Position int    `json:"position"`
		Format   string `json:"format"`
		Title    string `json:"title"`
		Tracks   []struct {
			Position     int            `json:"position"`
			ID           string         `json:"id"`
			Number       string         `json:"number"`
			Title        string         `json:"title"`
			Length       *int64         `json:"length"`
			ArtistCredit []ArtistCredit `json:"artist-credit"`
			Recording    *Entity        `json:"recording"`
		} `json:"tracks"`
	} `json:"media"`
	ReleaseGroup  *Entity  `json:"release-group"`
	Releases      []Entity `json:"releases"`
	ReleaseGroups []Entity `json:"release-groups"`
	Recordings    []Entity `json:"recordings"`
}

type ArtistCredit struct {
	Name       string `json:"name"`
	JoinPhrase string `json:"joinphrase"`
	Artist     struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"artist"`
}
