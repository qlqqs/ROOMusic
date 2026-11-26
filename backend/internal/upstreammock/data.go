package upstreammock

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
)

//go:embed testdata/catalog.json testdata/cover.png
var builtin embed.FS

type artist struct {
	ID      string      `json:"id"`
	Name    string      `json:"name"`
	Sort    string      `json:"sort-name"`
	Type    string      `json:"type"`
	Country string      `json:"country"`
	Note    string      `json:"disambiguation"`
	Aliases []object    `json:"aliases"`
	Media   artistMedia `json:"media"`
}
type group struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Type  string `json:"primary-type"`
	Date  string `json:"first-release-date"`
}
type artistCredit struct{ Artist, JoinPhrase string }

type recording struct {
	Credits                 []artistCredit
	ID, Title, Artist, ISRC string
	Length                  int
}
type medium struct {
	Disc   string
	Tracks []struct{ ID, Recording string }
}
type release struct {
	Credits                                                                 []artistCredit
	ID, Title, Artist, Group, Country, Date, Barcode, Label, Catalog, Image string
	Media                                                                   []medium
}
type lyric struct {
	ID       int    `json:"id"`
	Title    string `json:"trackName"`
	Artist   string `json:"artistName"`
	Album    string `json:"albumName"`
	Duration int    `json:"duration"`
	Plain    string `json:"plainLyrics"`
	Synced   string `json:"syncedLyrics"`
}
type dataset struct {
	Artists      []artist
	Groups       []group
	Recordings   []recording
	Releases     []release
	Lyrics       []lyric
	Fingerprints map[string][]string
	Search       []object
	covers       map[string][]byte
}

func decodeJSON(r io.Reader, v any) error {
	d := json.NewDecoder(r)
	if err := d.Decode(v); err != nil {
		return err
	}
	if err := d.Decode(new(json.RawMessage)); err != io.EOF {
		return errors.New("需要单个 JSON 值")
	}
	return nil
}

func loadData(dir string) (dataset, error) {
	files, err := fs.Sub(builtin, "testdata")
	if err != nil {
		return dataset{}, err
	}
	if dir != "" {
		files = os.DirFS(dir)
	}
	var data dataset
	raw, err := fs.ReadFile(files, "catalog.json")
	if err != nil {
		return data, err
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return data, fmt.Errorf("catalog.json: %w", err)
	}
	data.covers = map[string][]byte{}
	var images []string
	for _, rel := range data.Releases {
		images = append(images, rel.Image)
	}
	for _, a := range data.Artists {
		images = append(images, a.Media.Image)
	}
	for _, name := range images {
		if name == "" || data.covers[name] != nil {
			continue
		}
		img, err := fs.ReadFile(files, name)
		if err != nil {
			return data, err
		}
		if len(bytes.TrimSpace(img)) == 0 {
			return data, fmt.Errorf("图片为空 %s", name)
		}
		data.covers[name] = img
	}
	return data, nil
}

func (d dataset) artist(id string) artist {
	for _, v := range d.Artists {
		if v.ID == id {
			return v
		}
	}
	return artist{}
}
func (d dataset) group(id string) group {
	for _, v := range d.Groups {
		if v.ID == id {
			return v
		}
	}
	return group{}
}
func (d dataset) recording(id string) recording {
	for _, v := range d.Recordings {
		if v.ID == id {
			return v
		}
	}
	return recording{}
}
func (rel release) contains(id string) bool {
	for _, m := range rel.Media {
		for _, t := range m.Tracks {
			if t.Recording == id {
				return true
			}
		}
	}
	return false
}
func (rel release) trackCount() int {
	n := 0
	for _, m := range rel.Media {
		n += len(m.Tracks)
	}
	return n
}
