package airplay

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net/http"
)

type Metadata struct {
	Title, Artist, Album string
	Artwork              []byte
	ArtworkType          string
}

func (m *mediaSession) positionHeaders() http.Header {
	h := m.headers()
	seq, stamp := m.transport.Position()
	h.Set("RTP-Info", fmt.Sprintf("seq=%d;rtptime=%d", seq, stamp))
	return h
}

func (s *Sender) SetVolume(ctx context.Context, level float64, muted bool) error {
	if math.IsNaN(level) || level < 0 || level > 1 {
		return errors.New("AirPlay 音量无效")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(); err != nil {
		return err
	}
	if s.media != nil {
		if err := s.media.volume(ctx, level, muted); err != nil {
			return err
		}
	}
	s.vol, s.muted = &level, muted
	return nil
}

func (m *mediaSession) volume(ctx context.Context, level float64, muted bool) error {
	db := -30 + 30*level
	if muted || level == 0 {
		db = -144
	}
	h := m.headers()
	h.Set("Content-Type", "text/parameters")
	_, err := m.command(ctx, "SET_PARAMETER", m.uri, h, fmt.Appendf(nil, "volume: %.6f\r\n", db))
	return err
}

// 文本元数据由 DMAP 的 mlit 容器打包，封面与文本使用同一播放头。行为参考 pyatv（MIT）。
func tag(dst []byte, name string, value []byte) []byte {
	dst = append(dst, name...)
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(value)))
	return append(dst, value...)
}

func (s *Sender) SetMetadata(ctx context.Context, data Metadata) error {
	if len(data.Artwork) > 0 && data.ArtworkType != "image/jpeg" && data.ArtworkType != "image/png" {
		return errors.New("AirPlay 封面类型无效")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(); err != nil {
		return err
	}
	if s.media != nil {
		if err := s.media.metadata(ctx, data); err != nil {
			return err
		}
	}
	data.Artwork = bytes.Clone(data.Artwork)
	s.metadata = &data
	return nil
}

func (m *mediaSession) metadata(ctx context.Context, data Metadata) error {
	payload := tag(nil, "minm", []byte(data.Title))
	payload = tag(payload, "asar", []byte(data.Artist))
	payload = tag(payload, "asal", []byte(data.Album))
	h := m.positionHeaders()
	// 接收端可独立支持文字和封面，拒绝某种元数据仍可继续接收音频。
	send := func(mime string, body []byte) error {
		h.Set("Content-Type", mime)
		_, err := m.command(ctx, "SET_PARAMETER", m.uri, h, body)
		if statusIs(err, 405, 415, 451, 501) {
			return nil
		}
		return err
	}
	if err := send("application/x-dmap-tagged", tag(nil, "mlit", payload)); err != nil {
		return err
	}
	if len(data.Artwork) > 0 {
		return send(data.ArtworkType, data.Artwork)
	}
	return nil
}
