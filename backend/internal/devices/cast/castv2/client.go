package castv2

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/qlqq/roomusic/backend/internal/devices/lan"
)

const interval = 5 * time.Second

type Volume struct {
	ControlType string   `json:"controlType"`
	Level       *float64 `json:"level"`
	Muted       bool     `json:"muted"`
}

type Application struct {
	AppID       string `json:"appId"`
	TransportID string `json:"transportId"`
	SessionID   string `json:"sessionId"`
}

type ReceiverStatus struct {
	Applications []Application `json:"applications"`
	Volume       Volume        `json:"volume"`
}

type Envelope struct {
	Type      string          `json:"type"`
	RequestID int             `json:"requestId"`
	Status    json.RawMessage `json:"status"`
}

type Event struct {
	Message
	Envelope
	Order uint64
}

// 读协程按线上顺序编号，控制层可丢弃早于命令回执的旧广播。
type Client struct {
	conn         net.Conn
	sender       string
	writeMu      sync.Mutex
	disconnected sync.Once
	mu           sync.Mutex
	next         int
	pending      map[int]response
	events       chan Event
	done         chan struct{}
	pongs        atomic.Uint64
	wg           sync.WaitGroup
}

type response struct {
	source, namespace string
	ch                chan Event
}

func Dial(ctx context.Context, address string) (*Client, error) {
	addr, err := netip.ParseAddrPort(address)
	if err != nil || addr.Port() == 0 || !lan.IsLANAddress(addr.Addr()) {
		return nil, errors.New("Cast 地址必须是局域网 IPv4 和端口")
	}
	dial, cancel := context.WithTimeout(ctx, interval)
	defer cancel()
	// Cast 使用自签名接收端证书，TCP 目的地址已经固定为局域网字面地址。
	d := tls.Dialer{Config: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}}
	conn, err := d.DialContext(dial, "tcp4", addr.String())
	if err != nil {
		return nil, err
	}
	c := &Client{conn: conn, sender: "sender-" + rand.Text(), pending: map[int]response{}, events: make(chan Event, 64), done: make(chan struct{})}
	c.wg.Go(c.read)
	if err := c.Connect(ReceiverID); err != nil {
		c.Close()
		return nil, err
	}
	c.wg.Go(c.heartbeat)
	return c, nil
}

func (c *Client) Connect(dest string) error {
	return c.send(dest, Connection, map[string]any{"type": "CONNECT", "origin": map[string]any{}})
}

func (c *Client) Events() <-chan Event  { return c.events }
func (c *Client) Done() <-chan struct{} { return c.done }

// 心跳超时、读失败与写失败都先发布断线，托管输出才能及时标记离线。
func (c *Client) disconnect() {
	c.disconnected.Do(func() {
		close(c.done)
		c.conn.Close()
	})
}

func (c *Client) Close() {
	c.disconnect()
	c.wg.Wait()
}

func (c *Client) send(dest, ns string, body any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.conn.SetWriteDeadline(time.Now().Add(interval)); err != nil {
		c.disconnect()
		return err
	}
	if err := Write(c.conn, Message{Source: c.sender, Destination: dest, Namespace: ns, Payload: data}); err != nil {
		c.disconnect()
		return err
	}
	return nil
}

func (c *Client) read() {
	defer c.disconnect()
	var order uint64
	for {
		msg, err := Read(c.conn)
		if err != nil {
			return
		}
		if msg.Destination != c.sender && msg.Destination != "*" {
			continue
		}
		event := Event{Message: msg}
		if err := json.Unmarshal(msg.Payload, &event.Envelope); err != nil {
			continue
		}
		if msg.Namespace == Heartbeat {
			switch event.Type {
			case "PING":
				if err := c.send(msg.Source, Heartbeat, map[string]string{"type": "PONG"}); err != nil {
					return
				}
			case "PONG":
				if msg.Source == ReceiverID {
					c.pongs.Add(1)
				}
			}
			continue
		}
		order++
		event.Order = order
		c.mu.Lock()
		wait, ok := c.pending[event.RequestID]
		if ok && msg.Source == wait.source && msg.Namespace == wait.namespace {
			delete(c.pending, event.RequestID)
			wait.ch <- event
			c.mu.Unlock()
			continue
		}
		c.mu.Unlock()
		select {
		case c.events <- event:
		default:
			return
		}
	}
}

func (c *Client) heartbeat() {
	timer := time.NewTicker(interval)
	defer timer.Stop()
	var previous uint64
	missed := 0
	for {
		if err := c.send(ReceiverID, Heartbeat, map[string]string{"type": "PING"}); err != nil {
			c.disconnect()
			return
		}
		select {
		case <-c.done:
			return
		case <-timer.C:
		}
		count := c.pongs.Load()
		if count == previous {
			missed++
		} else {
			missed = 0
		}
		if missed >= 3 {
			c.disconnect()
			return
		}
		previous = count
	}
}

// Request 将请求与该命名空间、该接收者返回的 requestId 配对。
func (c *Client) Request(ctx context.Context, dest, ns string, body map[string]any) (Event, error) {
	if err := ctx.Err(); err != nil {
		return Event{}, err
	}
	c.mu.Lock()
	c.next++
	id := c.next
	wait := response{source: dest, namespace: ns, ch: make(chan Event, 1)}
	c.pending[id] = wait
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, id); c.mu.Unlock() }()
	body["requestId"] = id
	if err := c.send(dest, ns, body); err != nil {
		return Event{}, err
	}
	timer := time.NewTimer(2 * interval)
	defer timer.Stop()
	select {
	case e := <-wait.ch:
		return e, nil
	case <-c.done:
		return Event{}, net.ErrClosed
	case <-ctx.Done():
		return Event{}, ctx.Err()
	case <-timer.C:
		return Event{}, fmt.Errorf("Cast %v 等待回执超时", body["type"])
	}
}

func (e Event) DecodeStatus(kind string, target any) error {
	if e.Type != kind {
		return fmt.Errorf("Cast 请求返回 %s", e.Type)
	}
	return json.Unmarshal(e.Status, target)
}

func Probe(ctx context.Context, address string) (Volume, error) {
	ctx, cancel := context.WithTimeout(ctx, interval)
	defer cancel()
	c, err := Dial(ctx, address)
	if err != nil {
		return Volume{}, err
	}
	defer c.Close()
	e, err := c.Request(ctx, ReceiverID, Receiver, map[string]any{"type": "GET_STATUS"})
	if err != nil {
		return Volume{}, err
	}
	var status ReceiverStatus
	if err := e.DecodeStatus("RECEIVER_STATUS", &status); err != nil {
		return Volume{}, err
	}
	if v := status.Volume.Level; v != nil && (*v < 0 || *v > 1) {
		status.Volume.Level = nil
	}
	return status.Volume, nil
}

// Poll 返回的状态与主动广播共用有序事件通道。
func (c *Client) Poll(dest, ns string) error {
	c.mu.Lock()
	c.next++
	id := c.next
	c.mu.Unlock()
	return c.send(dest, ns, map[string]any{"type": "GET_STATUS", "requestId": id})
}
