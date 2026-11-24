package castv2

import (
	"encoding/json"
	"net"
	"testing"
	"testing/synctest"
	"time"
)

func pipeClient(t *testing.T) (*Client, net.Conn) {
	t.Helper()
	sender, peer := net.Pipe()
	client := &Client{conn: sender, sender: "sender-test", pending: map[int]response{}, events: make(chan Event, 8), done: make(chan struct{})}
	t.Cleanup(client.Close)
	t.Cleanup(func() { peer.Close() })
	client.wg.Go(client.read)
	return client, peer
}

func readCastMessage(t *testing.T, peer net.Conn, namespace, dest, kind string) (Message, Envelope) {
	t.Helper()
	m, err := Read(peer)
	if err != nil {
		t.Fatal(err)
	}
	var e Envelope
	if err := json.Unmarshal(m.Payload, &e); err != nil {
		t.Fatal(err)
	}
	if m.Namespace != namespace || m.Source != "sender-test" || m.Destination != dest || e.Type != kind {
		t.Fatalf("消息 = %+v %s，期望 %s -> %s %s", m, m.Payload, namespace, dest, kind)
	}
	return m, e
}

func sendCastMessage(t *testing.T, peer net.Conn, namespace, source, payload string) {
	t.Helper()
	if err := Write(peer, Message{Namespace: namespace, Source: source, Destination: "sender-test", Payload: []byte(payload)}); err != nil {
		t.Fatal(err)
	}
}

func TestCastClientNamespaceMessages(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, peer := pipeClient(t)
		peer.SetDeadline(time.Now().Add(time.Second))
		go func() {
			if err := c.Connect(ReceiverID); err != nil {
				t.Error(err)
			}
		}()
		message, _ := readCastMessage(t, peer, Connection, ReceiverID, "CONNECT")
		if string(message.Payload) != `{"origin":{},"type":"CONNECT"}` {
			t.Fatalf("CONNECT = %s", message.Payload)
		}
		result := make(chan Event, 1)
		go func() {
			e, err := c.Request(t.Context(), ReceiverID, Receiver, map[string]any{"type": "LAUNCH", "appId": DefaultApp})
			if err != nil {
				t.Error(err)
			}
			result <- e
		}()
		message, launch := readCastMessage(t, peer, Receiver, ReceiverID, "LAUNCH")
		if string(message.Payload) != `{"appId":"CC1AD845","requestId":1,"type":"LAUNCH"}` || launch.RequestID != 1 {
			t.Fatalf("LAUNCH = %s", message.Payload)
		}
		sendCastMessage(t, peer, Media, "transport-test", `{"type":"MEDIA_STATUS","requestId":1,"status":[{"playerState":"BUFFERING"}]}`)
		sendCastMessage(t, peer, Receiver, ReceiverID, `{"type":"RECEIVER_STATUS","requestId":1,"status":{"volume":{"controlType":"fixed","level":0.5},"applications":[{"appId":"CC1AD845","transportId":"transport-test","sessionId":"app-test"}]}}`)
		media := <-c.Events()
		var mediaStatus []struct{ PlayerState string }
		if err := media.DecodeStatus("MEDIA_STATUS", &mediaStatus); err != nil || len(mediaStatus) != 1 || mediaStatus[0].PlayerState != "BUFFERING" {
			t.Fatalf("媒体广播 = %+v，错误 %v", media, err)
		}
		receiver := <-result
		var status ReceiverStatus
		if err := receiver.DecodeStatus("RECEIVER_STATUS", &status); err != nil || receiver.Order <= media.Order ||
			len(status.Applications) != 1 || status.Applications[0].TransportID != "transport-test" ||
			status.Volume.ControlType != "fixed" || status.Volume.Level == nil || *status.Volume.Level != 0.5 {
			t.Fatalf("接收端回执 = %+v，错误 %v", status, err)
		}
		sendCastMessage(t, peer, Heartbeat, ReceiverID, `{"type":"PING"}`)
		readCastMessage(t, peer, Heartbeat, ReceiverID, "PONG")
	})
}

func TestCastHeartbeatDisconnectsAfterThreeMisses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, peer := pipeClient(t)
		peer.SetDeadline(time.Now().Add(5 * interval))
		c.wg.Go(c.heartbeat)
		readCastMessage(t, peer, Heartbeat, ReceiverID, "PING")
		sendCastMessage(t, peer, Heartbeat, ReceiverID, `{"type":"PONG"}`)
		for range 3 {
			readCastMessage(t, peer, Heartbeat, ReceiverID, "PING")
			select {
			case <-c.Done():
				t.Fatal("连续三次丢失回执前已断开")
			default:
			}
		}
		time.Sleep(interval - time.Millisecond)
		synctest.Wait()
		select {
		case <-c.Done():
			t.Fatal("第三个 PONG 的等待期限还未到")
		default:
		}
		time.Sleep(time.Millisecond)
		synctest.Wait()
		select {
		case <-c.Done():
		default:
			t.Fatal("连续三次 PING 未收到回执后仍保持连接")
		}
	})
}
