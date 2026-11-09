package httpapi

import (
	"net/http"
	"time"

	"github.com/qlqq/roomusic/backend/internal/zones"
)

const (
	eventHeartbeatInterval  = 15 * time.Second
	credentialCheckInterval = 60 * time.Second
)

func (h *Handlers) events(w http.ResponseWriter, r *http.Request, userID string) {
	// Service 原子地关联在线状态、生成首条快照并订阅；非法会话只作为控制端连接。
	sub, err := h.service.SubscribeOutput(r.Context(), userID, r.URL.Query().Get("output_session"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	defer sub.Close()
	r, finish := h.responseLifetime(w, r, sub.Done)
	defer finish()

	stopped := func() bool {
		select {
		case <-r.Context().Done():
			return true
		case <-h.service.Done():
			return true
		case <-sub.Done:
			return true
		default:
			return false
		}
	}
	writeEvent := func(event zones.Message) bool {
		payload := make([]byte, 0, len(event.Kind)+len(event.Data)+16)
		payload = append(payload, "event: "...)
		payload = append(payload, event.Kind...)
		payload = append(payload, "\ndata: "...)
		payload = append(payload, event.Data...)
		payload = append(payload, '\n', '\n')
		// 取到 Events 后可能已溢出；写前再检查，不能排空已失效的旧缓冲。
		return !stopped() && writeAndFlush(w, payload) == nil
	}
	streamingHeaders(w, "text/event-stream")
	w.WriteHeader(http.StatusOK)
	// 首快照写出后才启动定时器，首帧不与心跳竞争。
	select {
	case <-r.Context().Done():
		return
	case <-h.service.Done():
		return
	case <-sub.Done:
		return
	case first, ok := <-sub.Events:
		if !ok || !writeEvent(first) {
			return
		}
	}

	heartbeat, heartbeatTimer := h.eventTimer(eventHeartbeatInterval)
	credentials, credentialTimer := h.eventTimer(credentialCheckInterval)
	defer func() {
		heartbeatTimer.Stop()
		credentialTimer.Stop()
	}()
	nextCredentialCheck := h.service.Now().Add(credentialCheckInterval)
	verify := func() bool {
		user, err := h.guard.Authenticate(r)
		if err != nil || user.ID != userID {
			// 已经进入 SSE，只断流；不得往事件流中混入 401 JSON 或改响应头。
			return false
		}
		nextCredentialCheck = h.service.Now().Add(credentialCheckInterval)
		credentialTimer.Stop()
		credentials, credentialTimer = h.eventTimer(credentialCheckInterval)
		return true
	}
	verifyIfDue := func() bool {
		return h.service.Now().Before(nextCredentialCheck) || verify()
	}

	for {
		if stopped() {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-h.service.Done():
			return
		case <-sub.Done:
			return
		case <-credentials:
			if !verify() {
				return
			}
		case <-heartbeat:
			if !verifyIfDue() || stopped() || writeAndFlush(w, []byte(": ping\n\n")) != nil {
				return
			}
			heartbeat, heartbeatTimer = h.eventTimer(eventHeartbeatInterval)
		case event, ok := <-sub.Events:
			if !ok || stopped() || !verifyIfDue() || !writeEvent(event) {
				return
			}
		}
	}
}

// 定时器只发信号，不访问 ResponseWriter；服务的注入时钟可驱动心跳和凭据复核。
func (h *Handlers) eventTimer(delay time.Duration) (<-chan struct{}, zones.Timer) {
	ready := make(chan struct{}, 1)
	timer := h.service.AfterFunc(delay, func() { ready <- struct{}{} })
	return ready, timer
}
