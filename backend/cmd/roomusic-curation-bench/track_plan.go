package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/sources"
)

type albumMap struct {
	ID        string        `json:"id"`
	Directory string        `json:"directory"`
	Tracks    []corpusTrack `json:"tracks"`
}

type corpusTrack struct {
	File        string            `json:"file"`
	Disc        int               `json:"disc"`
	Position    int               `json:"position"`
	Title       string            `json:"title"`
	RecordingID string            `json:"recording_id,omitempty"`
	LengthMS    int64             `json:"length_ms"`
	Synthetic   bool              `json:"synthetic,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`
}

func planTracks(entry corpusEntry, release sources.Release) ([]corpusTrack, error) {
	var tracks []corpusTrack
	for _, m := range release.Media {
		if len(m.Tracks) != m.TrackCount {
			return nil, fmt.Errorf("第 %d 碟曲目表不完整", m.Position)
		}
		for _, t := range m.Tracks {
			if t.LengthMS == nil || *t.LengthMS <= 0 {
				return nil, fmt.Errorf("第 %d 碟第 %d 首缺少版本时长", m.Position, t.Position)
			}
			title := t.Title
			if title == "" {
				title = t.RecordingTitle
			}
			tags := trackTags(entry, release, m, t)
			ext := ".flac"
			switch entry.Tags {
			case "multidisc":
				// 同一目录中曲号从 1 重启，留给扫描侧推断碟号。
				delete(tags, "DISCNUMBER")
			case "double_lp":
				delete(tags, "DISCNUMBER")
				tags["TRACKNUMBER"] = t.Number
			case "compilation":
				delete(tags, "ALBUMARTIST")
			case "collaboration":
				replaceCredit(tags, "ALBUMARTIST", release.Artists, "、")
				artists := t.Artists
				if len(artists) == 0 {
					artists = release.Artists
				}
				replaceCredit(tags, "ARTIST", artists, " feat. ")
			case "romanized":
				tags["ALBUM"] = entry.LocalTitle
				tags["ALBUMARTIST"] = entry.LocalArtist
				releaseArtist, _ := creditTags(release.Artists)
				if tags["ARTIST"] == releaseArtist {
					tags["ARTIST"] = entry.LocalArtist
				}
			case "gbk":
				ext = ".mp3"
			}
			file := fmt.Sprintf("%02d-%02d - %s%s", m.Position, t.Position, fileLabel(title), ext)
			tracks = append(tracks, corpusTrack{File: file, Disc: m.Position, Position: t.Position, Title: title,
				RecordingID: t.RecordingID, LengthMS: *t.LengthMS, Tags: tags})
		}
	}
	if len(tracks) == 0 {
		return nil, errors.New("发行没有可生成的曲目")
	}
	switch entry.Tags {
	case "subset":
		n := max(4, (len(tracks)+1)/2)
		if n >= len(tracks) {
			return nil, errors.New("子集需要至少五首公开曲目")
		}
		tracks = tracks[:n]
	case "superset":
		const title = "尾声试音（合成附轨）"
		m := release.Media[len(release.Media)-1]
		position := tracks[len(tracks)-1].Position + 1
		t := sources.Track{Position: position, Title: title}
		tracks = append(tracks, corpusTrack{File: fmt.Sprintf("%02d-%02d - %s.flac", m.Position, position, title),
			Disc: m.Position, Position: position, Title: title, LengthMS: 30000, Synthetic: true,
			Tags: trackTags(entry, release, m, t)})
	}
	return tracks, nil
}

func replaceCredit(tags map[string]string, field string, credits []sources.Credit, join string) {
	if len(credits) < 2 {
		return
	}
	var names []string
	for _, c := range credits {
		names = append(names, c.Name)
	}
	tags[field] = strings.Join(names, join)
}
