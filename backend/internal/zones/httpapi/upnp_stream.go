package httpapi

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/netip"
	"strconv"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/httpserver"
	"github.com/qlqq/roomusic/backend/internal/zones"
)

type StreamArtwork func(ctx context.Context, resourceID string, head bool) (mime string, data []byte, err error)

func (h *Handlers) WithStreamArtwork(read StreamArtwork) *Handlers {
	h.streamArtwork = read
	return h
}

func (h *Handlers) upnpResource(artwork bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h.serveUPnPResource(w, r, artwork)
	}
}

func (h *Handlers) serveUPnPResource(w http.ResponseWriter, r *http.Request, artwork bool) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		httpserver.WriteError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "接口不支持该请求方法")
		return
	}
	source := identity.SourceOf(r.RemoteAddr)
	if h.streamThrottled(w, r, h.streamThrottle.Check(source)) {
		return
	}
	peer, err := netip.ParseAddrPort(r.RemoteAddr)
	ip := peer.Addr().Unmap()
	if err != nil {
		ip, _ = netip.ParseAddr(r.RemoteAddr)
		ip = ip.Unmap()
	}
	local := ip.IsLoopback() || ip.Is4() && (ip.IsPrivate() || ip.IsLinkLocalUnicast())
	var stream zones.UPnPStreamBody
	if local {
		stream, err = h.service.OpenUPnPStream(r.Context(), r.PathValue("token"), !artwork && r.Method == http.MethodGet)
	} else {
		err = zones.ErrStreamGone
	}
	if errors.Is(err, zones.ErrStreamGone) {
		if !h.streamThrottled(w, r, h.streamThrottle.Failed(source)) {
			httpserver.WriteError(w, r, http.StatusNotFound, "not_found", "音频流不存在")
		}
		return
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	if artwork {
		h.serveUPnPArtwork(w, r, stream.ArtworkID)
		return
	}
	streamingHeaders(w, stream.MimeType)
	w.Header().Set("Accept-Ranges", "none")
	w.Header().Set("transferMode.dlna.org", "Streaming")
	w.Header().Set("contentFeatures.dlna.org", "DLNA.ORG_OP=00;DLNA.ORG_CI=1;DLNA.ORG_FLAGS=01700000000000000000000000000000")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	defer stream.FinishDelivery()
	r, finish := h.responseLifetime(w, r, stream.Done)
	defer finish()
	if http.NewResponseController(w).Flush() == nil && copyStream(r.Context(), w, stream.Body) {
		stream.MarkDelivered()
	}
}

func (h *Handlers) serveUPnPArtwork(w http.ResponseWriter, r *http.Request, resourceID string) {
	if resourceID == "" || h.streamArtwork == nil {
		httpserver.WriteError(w, r, http.StatusNotFound, "not_found", "封面不存在")
		return
	}
	head := r.Method == http.MethodHead
	mime, data, err := h.streamArtwork(r.Context(), resourceID, head)
	if err != nil {
		httpserver.WriteError(w, r, http.StatusNotFound, "not_found", "封面不存在")
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if !head {
		_, _ = w.Write(data)
	}
}

func (h *Handlers) streamThrottled(w http.ResponseWriter, r *http.Request, err error) bool {
	if err == nil {
		return false
	}
	throttled := err.(*identity.ThrottledError)
	w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(throttled.RetryAfter.Seconds()))))
	httpserver.WriteError(w, r, http.StatusTooManyRequests, "too_many_attempts", "请稍后重试")
	return true
}
