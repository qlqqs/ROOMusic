package zones

import (
	"encoding/json"
	"strings"
	"sync"
)

// Message 保存不可变的 JSON 副本，订阅者不能修改控制面的内存状态。
type Message struct {
	Kind string
	Data json.RawMessage
}

type Hub struct {
	mu          sync.Mutex
	subscribers map[string]map[*Subscription]struct{}
	closed      bool
}
type Subscription struct {
	Events  <-chan Message
	channel chan Message
	hub     *Hub
	userID  string
}

func NewHub() *Hub { return &Hub{subscribers: map[string]map[*Subscription]struct{}{}} }
func (h *Hub) Subscribe(userID string) *Subscription {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch := make(chan Message, 64)
	sub := &Subscription{Events: ch, channel: ch, hub: h, userID: userID}
	if h.closed {
		close(ch)
		return sub
	}
	if h.subscribers[userID] == nil {
		h.subscribers[userID] = map[*Subscription]struct{}{}
	}
	h.subscribers[userID][sub] = struct{}{}
	return sub
}
func (sub *Subscription) Close() {
	h := sub.hub
	h.mu.Lock()
	defer h.mu.Unlock()
	h.remove(sub)
}
func (h *Hub) remove(sub *Subscription) {
	group := h.subscribers[sub.userID]
	if _, ok := group[sub]; ok {
		delete(group, sub)
		close(sub.channel)
		if len(group) == 0 {
			delete(h.subscribers, sub.userID)
		}
	}
}

// Publish 不等待消费者；满缓冲关闭订阅，B3 的 SSE 将据此结束连接。
func (h *Hub) Publish(userID, kind string, data any) error {
	encoded, err := json.Marshal(data)
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.subscribers[userID] {
		msg := Message{Kind: kind, Data: append(json.RawMessage(nil), encoded...)}
		select {
		case sub.channel <- msg:
		default:
			h.remove(sub)
		}
	}
	return nil
}
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	for _, group := range h.subscribers {
		for sub := range group {
			h.remove(sub)
		}
	}
}
func (s *Service) Subscribe(userID string) *Subscription {
	return s.hub.Subscribe(strings.ToLower(userID))
}
func (s *Service) publish(userID, kind string, data any) {
	if err := s.hub.Publish(userID, kind, data); err != nil {
		s.reportAsync("publish_"+kind, userID, "", err)
	}
}
func (s *Service) publishZone(userID string, zone Zone) {
	s.publish(userID, "zone", struct {
		Zone Zone `json:"zone"`
	}{zone})
}
func (s *Service) publishQueue(userID, zoneID string, change *QueueChange) {
	if change != nil {
		s.publish(userID, "queue", struct {
			ZoneID string       `json:"zone_id"`
			Change *QueueChange `json:"change"`
		}{zoneID, change})
	}
}
func (s *Service) publishDevice(userID string, device Device) {
	s.publish(userID, "device", struct {
		Device Device `json:"device"`
	}{device})
}
