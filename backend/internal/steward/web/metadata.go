package web

// 只解码整理专辑需要的字段，不把数据源的账户、注释、关系或任意扩展直接交给模型。
type mbEntity struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	Title            string     `json:"title"`
	Disambiguation   string     `json:"disambiguation"`
	Date             string     `json:"date"`
	FirstReleaseDate string     `json:"first-release-date"`
	Country          string     `json:"country"`
	Barcode          string     `json:"barcode"`
	Length           *int64     `json:"length"`
	ArtistCredit     []mbCredit `json:"artist-credit"`
	LabelInfo        []struct {
		Label struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"label"`
		CatalogNumber string `json:"catalog-number"`
	} `json:"label-info"`
	Media []struct {
		Position int    `json:"position"`
		Format   string `json:"format"`
		Title    string `json:"title"`
		Tracks   []struct {
			ID           string     `json:"id"`
			Number       string     `json:"number"`
			Title        string     `json:"title"`
			Length       *int64     `json:"length"`
			ArtistCredit []mbCredit `json:"artist-credit"`
			Recording    *mbEntity  `json:"recording"`
		} `json:"tracks"`
	} `json:"media"`
	ReleaseGroup  *mbEntity  `json:"release-group"`
	Releases      []mbEntity `json:"releases"`
	ReleaseGroups []mbEntity `json:"release-groups"`
	Recordings    []mbEntity `json:"recordings"`
}

type mbCredit struct {
	Name       string `json:"name"`
	JoinPhrase string `json:"joinphrase"`
	Artist     struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"artist"`
}

// Metadata 是经过裁剪的音乐实体，不包含数据源的任意字段。
type Metadata struct {
	MBID             string         `json:"mbid"`
	Title            string         `json:"title,omitempty"`
	Name             string         `json:"name,omitempty"`
	Disambiguation   string         `json:"disambiguation,omitempty"`
	Artists          []ArtistCredit `json:"artists,omitempty"`
	Date             string         `json:"date,omitempty"`
	FirstReleaseDate string         `json:"first_release_date,omitempty"`
	Country          string         `json:"country,omitempty"`
	Barcode          string         `json:"barcode,omitempty"`
	LengthMS         *int64         `json:"length_ms,omitempty"`
	Labels           []Label        `json:"labels,omitempty"`
	Media            []Medium       `json:"media,omitempty"`
	ReleaseGroup     *Metadata      `json:"release_group,omitempty"`
	Releases         []Metadata     `json:"releases,omitempty"`
	ReleaseGroups    []Metadata     `json:"release_groups,omitempty"`
	Recordings       []Metadata     `json:"recordings,omitempty"`
}

// ArtistCredit 保留署名和连接词，便于区分联合艺人。
type ArtistCredit struct {
	MBID       string `json:"mbid"`
	Name       string `json:"name"`
	JoinPhrase string `json:"join_phrase,omitempty"`
}

// Label 包含厂牌名称与发行编号。
type Label struct {
	MBID          string `json:"mbid"`
	Name          string `json:"name"`
	CatalogNumber string `json:"catalog_number"`
}

// Medium 是一个唱片介质上的曲目表。
type Medium struct {
	Position int     `json:"position"`
	Format   string  `json:"format,omitempty"`
	Title    string  `json:"title,omitempty"`
	Tracks   []Track `json:"tracks"`
}

// Track 区分曲目 MBID 与录音 MBID，时长统一为毫秒。
type Track struct {
	MBID      string         `json:"mbid"`
	Number    string         `json:"number"`
	Title     string         `json:"title"`
	LengthMS  *int64         `json:"length_ms,omitempty"`
	Artists   []ArtistCredit `json:"artists,omitempty"`
	Recording *Metadata      `json:"recording,omitempty"`
}

func projectCredits(credits []mbCredit) []ArtistCredit {
	result := make([]ArtistCredit, 0, len(credits))
	for _, credit := range credits {
		result = append(result, ArtistCredit{credit.Artist.ID, credit.Name, credit.JoinPhrase})
	}
	return result
}

func projectEntities(entities []mbEntity) []Metadata {
	result := make([]Metadata, 0, len(entities))
	for _, entity := range entities {
		result = append(result, projectMetadata(entity))
	}
	return result
}

func projectMetadata(entity mbEntity) Metadata {
	result := Metadata{MBID: entity.ID, Title: entity.Title, Name: entity.Name, Disambiguation: entity.Disambiguation,
		Artists: projectCredits(entity.ArtistCredit), Date: entity.Date, FirstReleaseDate: entity.FirstReleaseDate,
		Country: entity.Country, Barcode: entity.Barcode, LengthMS: entity.Length,
		Releases: projectEntities(entity.Releases), ReleaseGroups: projectEntities(entity.ReleaseGroups), Recordings: projectEntities(entity.Recordings)}
	if entity.ReleaseGroup != nil {
		group := projectMetadata(*entity.ReleaseGroup)
		result.ReleaseGroup = &group
	}
	for _, label := range entity.LabelInfo {
		result.Labels = append(result.Labels, Label{label.Label.ID, label.Label.Name, label.CatalogNumber})
	}
	for _, medium := range entity.Media {
		output := Medium{Position: medium.Position, Format: medium.Format, Title: medium.Title, Tracks: []Track{}}
		for _, track := range medium.Tracks {
			item := Track{MBID: track.ID, Number: track.Number, Title: track.Title, LengthMS: track.Length, Artists: projectCredits(track.ArtistCredit)}
			if track.Recording != nil {
				recording := projectMetadata(*track.Recording)
				item.Recording = &recording
			}
			output.Tracks = append(output.Tracks, item)
		}
		result.Media = append(result.Media, output)
	}
	return result
}
