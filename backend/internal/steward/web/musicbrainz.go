package web

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/egress"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

var coverArtBaseURL = "https://coverartarchive.org"

var includesByEntity = map[string][]string{
	"artist":        {"releases", "release-groups", "recordings"},
	"release":       {"artist-credits", "recordings", "labels", "release-groups"},
	"release-group": {"artist-credits", "releases"},
	"recording":     {"artist-credits", "releases"},
}

func musicUserAgent() string {
	return sources.UserAgent()
}

func musicError(err error) error {
	switch {
	case errors.Is(err, sources.ErrRateLimited):
		return failure("rate_limited")
	case errors.Is(err, sources.ErrNotFound), errors.Is(err, sources.ErrUnavailable), errors.Is(err, sources.ErrTryLater):
		return failure("source_unavailable")
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
	Metadata Metadata `json:"metadata"`
	Covers   []Cover  `json:"covers,omitempty"`
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
	response, err := s.music.Lookup(ctx, entity, mbid, inc)
	if err != nil {
		return MusicDetails{}, musicError(err)
	}
	result = MusicDetails{Metadata: projectMetadata(response)}
	if covers {
		var err error
		result.Covers, err = s.coverArt(ctx, strings.ToLower(mbid))
		if err != nil {
			return MusicDetails{}, err
		}
	}
	return result, nil
}

// Cover 仅包含封面地址和正背面标记，不包含图片字节。
type Cover struct {
	URL        string            `json:"url"`
	Front      bool              `json:"front"`
	Back       bool              `json:"back"`
	Thumbnails map[string]string `json:"thumbnails"`
}

func (s *Service) coverArt(ctx context.Context, mbid string) (covers []Cover, err error) {
	address := coverArtBaseURL + "/release/" + mbid + "/"
	started := time.Now()
	defer func() { s.logRequest(ctx, "cover_art_archive", address, started, len(covers), err) }()
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	client, err := s.newClient(egress.Options{Policy: egress.PublicOnly, MaxRedirects: 5, Timeout: requestTimeout})
	if err != nil {
		return nil, failure("source_unavailable")
	}
	defer client.CloseIdleConnections()
	redirect := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := redirect(req, via); err != nil {
			return err
		}
		host := strings.ToLower(req.URL.Hostname())
		// 官方清单接口会跳到 Archive.org 的 index.json；不跟随图片或其他站点。
		if validatePageURL(req.URL.String()) != nil || !(host == "archive.org" || strings.HasSuffix(host, ".archive.org") || host == "coverartarchive.org") || !strings.HasSuffix(req.URL.Path, "/index.json") {
			return failure("source_unavailable")
		}
		req.Header = make(http.Header)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", musicUserAgent())
		return nil
	}
	req, err := request(ctx, http.MethodGet, address, nil, musicUserAgent())
	if err != nil {
		return nil, failure("source_unavailable")
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, failure("source_unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return []Cover{}, nil
	}
	var payload struct {
		Images []struct {
			Image      string            `json:"image"`
			Front      bool              `json:"front"`
			Back       bool              `json:"back"`
			Thumbnails map[string]string `json:"thumbnails"`
		} `json:"images"`
	}
	if response.StatusCode != http.StatusOK || readJSON(response, &payload) != nil || payload.Images == nil {
		return nil, failure("source_unavailable")
	}
	covers = make([]Cover, 0, len(payload.Images))
	for _, image := range payload.Images {
		covers = append(covers, Cover{image.Image, image.Front, image.Back, image.Thumbnails})
	}
	return covers, nil
}
