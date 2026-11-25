package httpapi

import (
	"errors"
	"io"
	"net/http"

	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/playback"
	"github.com/qlqq/roomusic/backend/internal/playback/render"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

func (h *Handlers) stream(w http.ResponseWriter, r *http.Request) error {
	zone := r.Context().Value(clientZoneKey{}).(zones.Zone)
	if err := h.browse.Zones.TouchClientDevice(r.Context(), memberID(r), zone.DeviceID); err != nil {
		return err
	}
	return h.audio(w, r, zone.Settings, r.Form.Get("format") == "raw")
}

func (h *Handlers) download(w http.ResponseWriter, r *http.Request) error {
	return h.audio(w, r, zones.SubsonicSettings(), true)
}

// originalCodec 只匹配客户端可以直放的容器与编码组合。
func originalCodec(source catalog.TrackAudioFacts, requested string) bool {
	if source.Codec == nil {
		return false
	}
	codec := *source.Codec
	switch source.SourceKind {
	case "wav_info":
		return requested == "" || requested == "wav"
	case "m4a":
		return codec == "aac" && (requested == "" || requested == "aac" || requested == "m4a")
	case "ogg_vorbis_comment":
		return codec == "vorbis" && (requested == "" || requested == "vorbis" || requested == "ogg")
	case "flac_vorbis_comment", "mp3_id3v2", "opus_tags":
		return requested == "" || requested == codec
	}
	return false
}

func (h *Handlers) audio(w http.ResponseWriter, r *http.Request, settings zones.ZoneSettings, raw bool) error {
	id, err := entityID(r.Form)
	if err != nil {
		return err
	}
	summaries, err := h.browse.Catalog.LoadTrackSummaries(r.Context(), []string{id})
	if err != nil {
		return err
	}
	t := summaries[id]
	if !t.Present {
		return errNotFound
	}
	cue := t.Source.SourceKind == "cue_virtual"
	spec := zones.TrackRender{UserID: memberID(r), TrackID: id, Settings: settings}
	direct := raw && !cue
	if raw {
		spec.Codec = "flac"
		spec.Settings = zones.SubsonicSettings()
	} else {
		spec.Codec = r.Form.Get("format")
		spec.BitRate, err = number(r.Form, "maxBitRate", 0)
		if err != nil {
			return err
		}
		offset, err := number(r.Form, "timeOffset", 0)
		if err != nil {
			return err
		}
		spec.StartSeconds = float64(offset)
		direct = !cue && !render.HasProcessing(settings) && !r.Form.Has("timeOffset") && originalCodec(t.Source, spec.Codec) &&
			(spec.BitRate == 0 || t.Source.Bitrate != nil && spec.BitRate >= *t.Source.Bitrate)
		if !direct {
			switch spec.Codec {
			case "":
				spec.Codec = "flac"
				if spec.BitRate > 0 || settings.Quality == "lossy" {
					spec.Codec = "opus"
				}
			case "aac", "opus", "mp3", "flac":
			default:
				return errParameter
			}
		}
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if direct {
		opened, err := h.browse.Playback.OpenSource(r.Context(), id)
		if err != nil {
			return err
		}
		defer opened.File.Close()
		w.Header().Set("Content-Type", opened.Format.MIMEType)
		w.Header().Set("ETag", httpserver.EntityTag(opened.ETag))
		http.ServeContent(w, r, id, time.Time{}, opened.File)
		return nil
	}
	if h.browse.Renderer == nil {
		// 缺少渲染器时仍先核对目录状态，保持与直发相同的错误顺序。
		source, err := h.browse.Playback.OpenForRender(r.Context(), id)
		if err == nil {
			_ = source.File.Close()
		} else if !errors.Is(err, playback.ErrNotRenderable) {
			return err
		}
		return render.ErrUnavailable
	}
	body, format, err := h.browse.Renderer.RenderTrack(r.Context(), spec)
	if err != nil {
		return err
	}
	defer body.Close()
	w.Header().Set("Content-Type", format.MimeType)
	w.Header().Set("Accept-Ranges", "none")
	w.WriteHeader(http.StatusOK)
	_ = http.NewResponseController(w).Flush()
	_, err = io.Copy(w, body)
	if err != nil && r.Context().Err() == nil {
		panic(http.ErrAbortHandler)
	}
	return nil
}
