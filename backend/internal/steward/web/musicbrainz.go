package web

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/egress"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/musicbrainz"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

var includesByEntity = map[string][]string{
	"artist":        {"releases", "release-groups", "recordings", "aliases"},
	"release":       {"artist-credits", "recordings", "labels", "release-groups", "media", "discids", "isrcs"},
	"release-group": {"artist-credits", "releases"},
	"recording":     {"artist-credits", "releases", "isrcs"},
}

func musicError(err error) error {
	switch {
	case errors.Is(err, sources.ErrRateLimited):
		return errors.Join(failure("rate_limited"), err)
	case errors.Is(err, sources.ErrNotFound):
		return &tool.Error{Code: tool.CodeTargetNotFound, Message: "来源没有这条记录"}
	case errors.Is(err, egress.ErrDenied):
		return failure("egress_denied")
	case errors.Is(err, sources.ErrUnavailable), errors.Is(err, sources.ErrTryLater):
		return errors.Join(failure("source_unavailable"), err)
	default:
		return err
	}
}

// SearchMusic 查找整理专辑所需的有限音乐实体字段。
func (s *Service) SearchMusic(ctx context.Context, entity, query string, limit int) (results []Metadata, err error) {
	started := time.Now()
	defer func() { s.logRequest(ctx, "musicbrainz", s.music.BaseURL(), started, len(results), err) }()
	if _, ok := includesByEntity[entity]; !ok || strings.TrimSpace(query) == "" || limit < 1 || limit > 10 {
		return nil, &tool.Error{Code: tool.CodeInvalidInput, Message: "音乐实体、查询词或结果数无效"}
	}
	entities, err := s.music.Search(ctx, entity, query, limit)
	if err != nil {
		return nil, musicError(err)
	}
	return projectEntities(entities), nil
}

// MusicDetails 将 MusicBrainz 元数据与可选封面清单分开，不下载图片。
type MusicDetails struct {
	Metadata   Metadata `json:"metadata"`
	NextOffset *int     `json:"next_offset,omitempty"`
	Covers     []Cover  `json:"covers,omitempty"`
}

// GetMusic 仅接受 MBID 和按实体限定的 include 白名单；cover-art 只适用于 release。
func (s *Service) GetMusic(ctx context.Context, entity, mbid string, include []string) (result MusicDetails, err error) {
	started := time.Now()
	defer func() {
		count := 0
		if err == nil {
			count = 1
		}
		s.logRequest(ctx, "musicbrainz", s.music.BaseURL(), started, count, err)
	}()
	if entity == "release_group" {
		entity = "release-group"
		include = append(slices.Clone(include), "releases")
	}
	allowed, ok := includesByEntity[entity]
	if !ok || !uuid.Valid(mbid) {
		return MusicDetails{}, &tool.Error{Code: tool.CodeInvalidInput, Message: "音乐实体或 MBID 无效"}
	}
	var inc []string
	covers := false
	seen := make(map[string]bool)
	for _, value := range include {
		if seen[value] {
			continue
		}
		seen[value] = true
		if value == "cover-art" && entity == "release" {
			covers = true
			continue
		}
		valid := false
		for _, candidate := range allowed {
			if value == candidate {
				valid = true
				break
			}
		}
		if !valid {
			return MusicDetails{}, &tool.Error{Code: tool.CodeInvalidInput, Message: "include 包含该实体不支持的字段"}
		}
		inc = append(inc, value)
	}
	var response musicbrainz.Entity
	if entity == "release-group" && slices.Contains(inc, "releases") {
		response, err = s.music.ReleaseGroupDetails(ctx, strings.ToLower(mbid), true, inc...)
	} else {
		response, err = s.music.Lookup(ctx, entity, mbid, inc)
	}
	if err != nil {
		return MusicDetails{}, musicError(err)
	}
	result = MusicDetails{Metadata: projectMetadata(response)}
	if entity == "artist" && slices.Contains(inc, "aliases") {
		for _, alias := range response.Aliases {
			result.Metadata.Aliases = append(result.Metadata.Aliases, alias.Name)
		}
	}

	if covers {
		var err error
		result.Covers, err = s.coverArt(ctx, strings.ToLower(mbid))
		if err != nil {
			return MusicDetails{}, err
		}
	}
	return result, nil
}

// 长曲表与版本列表按原始顺序续读；各碟保留位置和完整曲数。
func (d *MusicDetails) page(offset int) {
	const pageSize = 25
	if editions := d.Metadata.Releases; len(editions) > 0 {
		start := min(offset, len(editions))
		end := start + min(pageSize, len(editions)-start)
		d.Metadata.Releases = editions[start:end]
		if end < len(editions) {
			d.NextOffset = &end
		}
		return
	}
	remaining := pageSize
	skip := offset
	for i := range d.Metadata.Media {
		disc := &d.Metadata.Media[i]
		start := min(skip, len(disc.Tracks))
		skip -= start
		n := min(remaining, len(disc.Tracks)-start)
		if start+n < len(disc.Tracks) {
			next := offset + pageSize
			d.NextOffset = &next
		}
		disc.Tracks = disc.Tracks[start : start+n]
		remaining -= n
	}
}

// Cover 仅包含封面地址和正背面标记，不包含图片字节。
type Cover struct {
	URL        string            `json:"url"`
	Front      bool              `json:"front"`
	Back       bool              `json:"back"`
	Thumbnails map[string]string `json:"thumbnails"`
}

func (s *Service) coverArt(ctx context.Context, mbid string) (covers []Cover, err error) {
	started := time.Now()
	defer func() { s.logRequest(ctx, "cover_art_archive", s.covers.BaseURL(), started, len(covers), err) }()
	images, err := s.covers.Covers(ctx, sources.CoverTarget{Kind: "release", ID: mbid})
	if err != nil {
		if errors.Is(err, egress.ErrDenied) {
			return nil, failure("source_unavailable")
		}
		return nil, musicError(err)
	}
	covers = make([]Cover, 0, len(images))
	for _, image := range images {
		covers = append(covers, Cover{URL: image.URL, Front: image.Front, Back: image.Back, Thumbnails: image.Thumbnails})
	}
	return covers, nil
}
