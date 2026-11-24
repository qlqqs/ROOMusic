package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/sources"
)

func (p *Source) Declaration() sources.Declaration {
	d := sources.Declaration{ID: "plugin:" + p.Manifest.ID, Name: p.Manifest.Name, Capabilities: p.Capabilities}
	for i, u := range p.Manifest.Upstreams {
		v := time.Duration(u.MinIntervalMS) * time.Millisecond
		if i == 0 || v < d.MinInterval {
			d.MinInterval = v
		}
		d.Upstreams = append(d.Upstreams, u.BaseURL)
	}
	for _, k := range p.Manifest.Keys {
		d.Keys = append(d.Keys, k.Name)
		if k.Optional {
			d.OptionalKeys = append(d.OptionalKeys, k.Name)
		}
	}
	return d
}
func (p *Source) current() bool {
	i := p.store.index(p.Manifest.ID)
	return i >= 0 && p.store.installed[i] == p && !p.blocked.Load()
}

func (p *Source) call(ctx context.Context, name string, input any, out any) error {
	s := p.store
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !p.current() {
		return sources.ErrNotFound
	}
	var loadErr error
	load := func(ctx context.Context) ([]byte, error) {
		if loadErr = sources.CheckPermit(ctx); loadErr != nil {
			return nil, loadErr
		}
		var wasm []byte
		wasm, loadErr = p.verified(ctx)
		return wasm, loadErr
	}
	b, err := json.Marshal(input)
	if err != nil {
		return err
	}
	r, err := s.engine.Call(ctx, p.Manifest.ID, load, name, b, p.fetch)
	if errors.Is(loadErr, ErrInvalid) {
		return sources.ErrNotFound
	}
	if loadErr != nil {
		return loadErr
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil {
		switch r.Code {
		case 0:
			if strict(r.Output, out) {
				return nil
			}
		case 1:
			return sources.ErrNotFound
		case 2:
			return sources.ErrTryLater
		}
	}
	return p.invalid(ctx, name)
}

func (p *Source) invalid(ctx context.Context, name string) error {
	p.store.logger.WarnContext(ctx, "插件调用失败或返回值无效", "plugin", p.Manifest.ID, "function", name)
	return sources.ErrTryLater
}

func (p *Source) Covers(ctx context.Context, t sources.CoverTarget) ([]sources.CoverImage, error) {
	var r struct {
		URL  string `json:"url"`
		MIME string `json:"mime"`
	}
	err := p.call(ctx, "cover_art", map[string]string{"kind": t.Kind, "mbid": t.ID, "title": t.Title, "artist": t.Artist}, &r)
	if err != nil {
		return nil, err
	}
	if _, ok := webURL(r.URL); !ok || len(r.MIME) > 128 {
		return nil, p.invalid(ctx, "cover_art")
	}
	return []sources.CoverImage{{ID: sources.ImageURLID(r.URL), URL: r.URL, Front: true}}, nil
}

func (p *Source) FrontCover(ctx context.Context, t sources.CoverTarget) (sources.CoverFile, error) {
	images, err := p.Covers(ctx, t)
	if err != nil {
		return sources.CoverFile{}, err
	}
	b, err := p.image(ctx, images[0].URL)
	return sources.CoverFile{Image: images[0], Data: b}, err
}

func (p *Source) LyricsFor(ctx context.Context, q sources.LyricsQuery) (sources.Lyrics, error) {
	var r struct {
		Kind      string `json:"kind"`
		Text      string `json:"text"`
		SourceURL string `json:"source_url"`
		ID        string `json:"external_id"`
	}
	err := p.call(ctx, "lyrics", map[string]any{"artist": q.Artist, "album": q.Album, "title": q.Title, "duration_seconds": q.DurationSeconds}, &r)
	if err != nil {
		return sources.Lyrics{}, err
	}
	if _, ok := webURL(r.SourceURL); !ok || !label(r.ID, 128) || len(r.Text) > 65536 || strings.TrimSpace(r.Text) == "" || strings.ContainsRune(r.Text, 0) || strings.Count(r.Text, "\n") >= 2000 {
		return sources.Lyrics{}, p.invalid(ctx, "lyrics")
	}
	l := sources.Lyrics{ID: r.ID, Title: q.Title, Album: q.Album, Artist: q.Artist, DurationSeconds: float64(q.DurationSeconds)}
	switch r.Kind {
	case "synced":
		l.Synced = r.Text
	case "plain":
		l.Plain = r.Text
	default:
		return sources.Lyrics{}, p.invalid(ctx, "lyrics")
	}
	return l, nil
}

func (p *Source) SearchLyrics(context.Context, sources.LyricsQuery) ([]sources.Lyrics, error) {
	return nil, sources.ErrNotFound
}

type credit struct {
	SourceURL  string `json:"source_url"`
	ID         string `json:"external_id"`
	License    string `json:"license,omitempty"`
	LicenseURL string `json:"license_url,omitempty"`
}

func (p *Source) credit(c credit) (sources.Attribution, bool) {
	_, ok := webURL(c.SourceURL)
	if !label(c.ID, 128) || !ok || len(c.License) > 512 {
		return sources.Attribution{}, false
	}
	if c.LicenseURL != "" {
		if _, ok = webURL(c.LicenseURL); !ok {
			return sources.Attribution{}, false
		}
	}
	return sources.Attribution{Source: "plugin:" + p.Manifest.ID, SourceURL: c.SourceURL, License: sources.PlainText(c.License), LicenseURL: c.LicenseURL}, true
}

func (p *Source) ArtistImage(ctx context.Context, l sources.ArtistLinks) (sources.ArtistImage, error) {
	var r struct {
		credit
		URL    string `json:"url"`
		Author string `json:"author,omitempty"`
	}
	err := p.call(ctx, "artist_image", map[string]string{"mbid": l.MBID, "name": l.Name, "wikidata": l.WikidataID}, &r)
	if err != nil {
		return sources.ArtistImage{}, err
	}
	c, ok := p.credit(r.credit)
	if !ok || len(r.Author) > 512 {
		return sources.ArtistImage{}, p.invalid(ctx, "artist_image")
	}
	c.Author = sources.PlainText(r.Author)
	b, err := p.image(ctx, r.URL)
	return sources.ArtistImage{ID: r.ID, Attribution: c, Data: b}, err
}

func (p *Source) ArtistBio(ctx context.Context, l sources.ArtistLinks, lang string) (sources.ArtistBio, error) {
	var r struct {
		credit
		Text string `json:"text"`
		Lang string `json:"lang"`
	}
	err := p.call(ctx, "artist_bio", map[string]string{"mbid": l.MBID, "name": l.Name, "lang": lang}, &r)
	if err != nil {
		return sources.ArtistBio{}, err
	}
	c, ok := p.credit(r.credit)
	txt := sources.BioText(r.Text)
	if !ok || txt == "" || r.Lang != lang || strings.ContainsRune(txt, 0) {
		return sources.ArtistBio{}, p.invalid(ctx, "artist_bio")
	}
	return sources.ArtistBio{Attribution: c, ID: r.ID, Text: txt, Lang: lang}, nil
}
