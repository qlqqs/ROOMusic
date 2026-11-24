package rtsp

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
)

func TestAirPlayRTSPPlainThenEncrypted(t *testing.T) {
	conn, peer := net.Pipe()
	t.Cleanup(func() { conn.Close(); peer.Close() })
	client := &Client{conn: conn, reader: bufio.NewReader(conn), writer: conn}
	readKey, writeKey := bytes.Repeat([]byte{0x22}, 32), bytes.Repeat([]byte{0x33}, 32)
	readCipher, _ := chacha20poly1305.New(readKey)
	writeCipher, _ := chacha20poly1305.New(writeKey)
	body := strings.Repeat("pcm", 400)
	result := make(chan error, 1)
	go func() {
		peer.SetDeadline(time.Now().Add(3 * time.Second))
		plain := bufio.NewReader(peer)
		if err := expectRequest(plain, "OPTIONS * RTSP/1.0", "1", ""); err != nil {
			result <- err
			return
		}
		if _, err := io.WriteString(peer, "RTSP/1.0 200 OK\r\nCSeq: 1\r\nContent-Length: 2\r\n\r\nOK"); err != nil {
			result <- err
			return
		}
		encrypted := bufio.NewReader(&recordReader{recordCipher: recordCipher{AEAD: writeCipher}, r: plain})
		if err := expectRequest(encrypted, "POST /feedback RTSP/1.0", "2", body); err != nil {
			result <- err
			return
		}
		writer := &recordWriter{recordCipher: recordCipher{AEAD: readCipher}, w: peer}
		_, err := fmt.Fprintf(writer, "RTSP/1.0 200 OK\r\nCSeq: 2\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
		result <- err
	}()
	resp, err := client.Do(t.Context(), "OPTIONS", "*", nil, nil)
	if err != nil || resp.StatusCode != 200 || string(resp.Body) != "OK" {
		t.Fatalf("明文 RTSP 响应 = %+v，错误 %v", resp, err)
	}
	if err := client.EnableEncryption(writeKey, readKey); err != nil {
		t.Fatal(err)
	}
	resp, err = client.Do(t.Context(), "POST", "/feedback", nil, []byte(body))
	if err != nil || resp.StatusCode != 200 || string(resp.Body) != body {
		t.Fatalf("密文 RTSP 响应错误 %v", err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func expectRequest(r *bufio.Reader, start, seq, body string) error {
	line, err := r.ReadString('\n')
	if err != nil || line != start+"\r\n" {
		return fmt.Errorf("请求行 = %q，错误 %v", line, err)
	}
	headers := make(map[string]string)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return err
		}
		if line == "\r\n" {
			break
		}
		k, v, _ := strings.Cut(line, ":")
		headers[k] = strings.TrimSpace(v)
	}
	if headers["CSeq"] != seq || headers["User-Agent"] != UserAgent ||
		(body != "" && headers["Content-Length"] != fmt.Sprint(len(body))) {
		return fmt.Errorf("RTSP 请求头 = %v", headers)
	}
	received := make([]byte, len(body))
	if _, err := io.ReadFull(r, received); err != nil {
		return err
	}
	if string(received) != body {
		return fmt.Errorf("RTSP 请求体不匹配，读取 %d 字节", len(received))
	}
	return nil
}

func TestAirPlayRTSPRejectsCorruptTruncatedAndReplayRecords(t *testing.T) {
	key := make([]byte, chacha20poly1305.KeySize)
	aead, _ := chacha20poly1305.New(key)
	// 独立构造首个线上帧，长度作为 AAD，首包 nonce 为零。
	prefix := []byte{3, 0}
	frame := aead.Seal(bytes.Clone(prefix), make([]byte, 12), []byte("one"), prefix)
	wrongTag := bytes.Clone(frame)
	wrongTag[len(wrongTag)-1] ^= 0x01
	for _, tc := range []struct {
		name  string
		wire  []byte
		prior string
	}{
		{"wrong tag", wrongTag, ""},
		{"short prefix", frame[:1], ""},
		{"short tag", frame[:len(frame)-1], ""},
		{"oversized record", []byte{1, 4}, ""},
		{"replay", append(bytes.Clone(frame), frame...), "one"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := &recordReader{recordCipher: recordCipher{AEAD: aead}, r: bytes.NewReader(tc.wire)}
			got, err := io.ReadAll(reader)
			if err == nil || string(got) != tc.prior {
				t.Fatalf("读取出 %q，错误 %v，期望保留 %q 并拒绝剩余帧", got, err, tc.prior)
			}
		})
	}
}
