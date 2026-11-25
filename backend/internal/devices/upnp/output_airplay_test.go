package upnp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/sha512"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/textproto"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/devices/airplay"
	"github.com/qlqq/roomusic/backend/internal/devices/airplay/bplist"
	"github.com/qlqq/roomusic/backend/internal/ptp"
	"github.com/qlqq/roomusic/backend/internal/zones"
	"golang.org/x/crypto/chacha20poly1305"
)

func TestAirPlayOutputWarnsOnLivePTPFallback(t *testing.T) {
	receiver := newFallbackReceiver(t)
	var available atomic.Bool
	available.Store(true)
	clock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		status := ptp.Status{Available: available.Load(), Role: "master", ClockID: 42, GrandmasterID: 42}
		if !status.Available {
			status.Reason = "clock_unlocked"
		}
		_ = json.NewEncoder(w).Encode(status)
	}))
	t.Cleanup(clock.Close)
	logger, logs := newFallbackTestLogger()
	saved := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(saved) })

	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	device := airplay.Device{Key: "airplay:02:00:00:00:00:42",
		Info: airplay.Info{AirPlayAddress: receiver, Features: 1 << 41}}
	sender, err := airplay.OpenSender(ctx, device, airplay.SenderConfig{
		PTP: &ptp.Client{Address: strings.TrimPrefix(clock.URL, "http://")},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	if got := sender.TimingProtocol(); got != "PTP" {
		t.Fatalf("初始计时协议 = %s，期望 PTP", got)
	}
	registry := NewRegistry()
	registry.Remember(Device{UDN: device.Key})
	output := &airplayOutput{
		deviceOutput: &deviceOutput{ctx: ctx, zoneID: "fixture-zone", manager: &outputManager{logger: logger}},
		sender:       sender, receiver: device, registry: registry,
	}
	sub := zones.NewHub().Subscribe("fixture-listener")
	defer sub.Close()
	exited := make(chan error, 1)
	go func() { exited <- output.serve(sub) }()
	defer func() {
		cancel()
		if err := <-exited; err != nil {
			t.Errorf("AirPlay 输出提前结束：%v", err)
		}
	}()

	available.Store(false)
	pending := map[string]bool{"airplay.sender.fallback": true, "airplay.output.fallback": true}
	for len(pending) != 0 {
		select {
		case record := <-logs:
			if !pending[record.Event] || record.Level != "WARN" || record.UDN != device.Key ||
				!strings.Contains(record.Error, "clock_unlocked") ||
				(record.Event == "airplay.output.fallback" && record.ZoneID != output.zoneID) {
				t.Fatalf("降级日志字段不匹配：%+v", record)
			}
			delete(pending, record.Event)
		case <-ctx.Done():
			t.Fatalf("降级计时协议 = %s，缺少日志 %v", sender.TimingProtocol(), pending)
		}
	}
	if got := sender.TimingProtocol(); got != "NTP" {
		t.Fatalf("降级后的计时协议 = %s，期望 NTP", got)
	}
}

type fallbackLog struct {
	Event, Level, UDN, Error string
	ZoneID                   string `json:"zone_id"`
}

type fallbackLogSink chan fallbackLog

func newFallbackTestLogger() (*slog.Logger, fallbackLogSink) {
	logs := make(fallbackLogSink, 4)
	return slog.New(slog.NewJSONHandler(logs, nil)), logs
}

func (logs fallbackLogSink) Write(data []byte) (int, error) {
	var record fallbackLog
	if err := json.Unmarshal(data, &record); err != nil {
		return 0, err
	}
	switch record.Event {
	case "airplay.sender.fallback", "airplay.output.fallback":
		select {
		case logs <- record:
		default:
			return 0, errors.New("降级日志超出预期")
		}
	}
	return len(data), nil
}

func newFallbackReceiver(t *testing.T) string {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	for _, addr := range addrs {
		prefix, err := netip.ParsePrefix(addr.String())
		if err != nil || !IsLANAddress(prefix.Addr()) {
			continue
		}
		bind := netip.AddrPortFrom(prefix.Addr(), 0).String()
		control, err := net.Listen("tcp4", bind)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { control.Close() })
		events, err := net.Listen("tcp4", bind)
		if err != nil {
			t.Fatal(err)
		}
		var workers sync.WaitGroup
		workers.Go(func() {
			for {
				conn, err := events.Accept()
				if err != nil {
					return
				}
				workers.Go(func() {
					defer conn.Close()
					_ = conn.SetReadDeadline(time.Now().Add(12 * time.Second))
					_, _ = io.Copy(io.Discard, conn)
				})
			}
		})
		workers.Go(func() {
			for {
				conn, err := control.Accept()
				if err != nil {
					return
				}
				workers.Go(func() {
					defer conn.Close()
					_ = conn.SetDeadline(time.Now().Add(12 * time.Second))
					if err := serveFallbackReceiver(conn, events.Addr().(*net.TCPAddr).Port); err != nil && !errors.Is(err, io.EOF) {
						t.Errorf("合成 AirPlay 接收端：%v", err)
					}
				})
			}
		})
		t.Cleanup(func() { control.Close(); events.Close(); workers.Wait() })
		return control.Addr().String()
	}
	t.Skip("AirPlay 协商需要本机局域网 IPv4 地址")
	return ""
}

type fallbackRequest struct {
	line, seq string
	body      []byte
}

func readFallbackRequest(r *textproto.Reader) (fallbackRequest, error) {
	line, err := r.ReadLine()
	if err != nil {
		return fallbackRequest{}, err
	}
	header, err := r.ReadMIMEHeader()
	if err != nil {
		return fallbackRequest{}, err
	}
	var body []byte
	if length := header.Get("Content-Length"); length != "" {
		n, err := strconv.Atoi(length)
		if err != nil || n < 0 || n > 4096 {
			return fallbackRequest{}, fmt.Errorf("无效的测试请求长度 %q", length)
		}
		body = make([]byte, n)
		if _, err := io.ReadFull(r.R, body); err != nil {
			return fallbackRequest{}, err
		}
	}
	return fallbackRequest{line: line, seq: header.Get("CSeq"), body: body}, nil
}

func writeFallbackReply(w io.Writer, protocol, seq string, body []byte) error {
	packet := fmt.Appendf(nil, "%s 200 OK\r\nCSeq: %s\r\nContent-Length: %d\r\n\r\n", protocol, seq, len(body))
	_, err := w.Write(append(packet, body...))
	return err
}

func serveFallbackReceiver(conn net.Conn, eventPort int) error {
	plain := bufio.NewReader(conn)
	reader := textproto.NewReader(plain)
	secret, err := pairFallbackReceiver(reader, conn)
	if err != nil {
		return err
	}
	read := newFallbackRecords(secret, "Control-Write-Encryption-Key")
	read.reader = plain
	write := newFallbackRecords(secret, "Control-Read-Encryption-Key")
	write.writer = conn
	reader = textproto.NewReader(bufio.NewReader(read))
	for {
		req, err := readFallbackRequest(reader)
		if err != nil {
			return err
		}
		method, _, _ := strings.Cut(req.line, " ")
		var response map[string]any
		switch method {
		case "GET":
			response = map[string]any{}
		case "SETUP":
			var setup map[string]any
			if err := bplist.Unmarshal(req.body, &setup); err != nil {
				return err
			}
			if _, stream := setup["streams"]; stream {
				response = map[string]any{"streams": []any{map[string]any{"dataPort": 49000, "controlPort": 49001}}}
			} else {
				response = map[string]any{"eventPort": eventPort}
			}
		case "RECORD", "SETPEERS", "POST", "TEARDOWN":
		default:
			return fmt.Errorf("意外请求 %q", req.line)
		}
		var body []byte
		if response != nil {
			body, err = bplist.Marshal(response)
			if err != nil {
				return err
			}
		}
		if err := writeFallbackReply(write, "RTSP/1.0", req.seq, body); err != nil {
			return err
		}
		if method == "TEARDOWN" {
			return nil
		}
	}
}

// 使用与 hap 的 RFC 5054 测试相同的群和证明，协商一条真正的测试控制连接。
func pairFallbackReceiver(r *textproto.Reader, w io.Writer) ([]byte, error) {
	const group = "FFFFFFFFFFFFFFFFC90FDAA22168C234C4C6628B80DC1CD129024E08" +
		"8A67CC74020BBEA63B139B22514A08798E3404DDEF9519B3CD3A431B" +
		"302B0A6DF25F14374FE1356D6D51C245E485B576625E7EC6F44C42E9" +
		"A637ED6B0BFF5CB6F406B7EDEE386BFB5A899FA5AE9F24117C4B1FE6" +
		"49286651ECE45B3DC2007CB8A163BF0598DA48361C55D39A69163FA8" +
		"FD24CF5F83655D23DCA3AD961C62F356208552BB9ED529077096966D" +
		"670C354E4ABC9804F1746C08CA18217C32905E462E36CE3BE39E772C" +
		"180E86039B2783A2EC07A28FB5C55DF06F4C52C9DE2BCBF695581718" +
		"3995497CEA956AE515D2261898FA051015728E5A8AAAC42DAD33170D" +
		"04507A33A85521ABDF1CBA64ECFB850458DBEF0A8AEA71575D060C7D" +
		"B3970F85A6E1E4C7ABF5AE8CDB0933D71E8C94E04A25619DCEE3D226" +
		"1AD2EE6BF12FFA06D98A0864D87602733EC86A64521F2B18177B200C" +
		"BBE117577A615D6C770988C0BAD946E208E24FA074E5AB3143DB5BFC" +
		"E0FD108E4B82D120A93AD2CAFFFFFFFFFFFFFFFF"
	first, err := readFallbackRequest(r)
	if err != nil {
		return nil, err
	}
	if first.line != "POST /pair-setup HTTP/1.1" {
		return nil, fmt.Errorf("意外的 HAP 配对请求 %q", first.line)
	}
	prime, _ := new(big.Int).SetString(group, 16)
	generator, exponent := big.NewInt(5), big.NewInt(37)
	pad := func(n *big.Int) []byte { return n.FillBytes(make([]byte, len(prime.Bytes()))) }
	salt := []byte("synthetic-clock")
	x := new(big.Int).SetBytes(fallbackSHA512(salt, fallbackSHA512([]byte("Pair-Setup:3939"))))
	verifier := new(big.Int).Exp(generator, x, prime)
	k := new(big.Int).SetBytes(fallbackSHA512(prime.Bytes(), pad(generator)))
	public := new(big.Int).Mod(new(big.Int).Add(new(big.Int).Exp(generator, exponent, prime), k.Mul(k, verifier)), prime)
	publicBytes := public.Bytes()
	m2 := append([]byte{6, 1, 2, 2, byte(len(salt))}, salt...)
	for part := range slices.Chunk(publicBytes, 255) {
		m2 = append(append(m2, 3, byte(len(part))), part...)
	}
	if err := writeFallbackReply(w, "HTTP/1.1", first.seq, m2); err != nil {
		return nil, err
	}
	third, err := readFallbackRequest(r)
	if err != nil {
		return nil, err
	}
	var clientPublic, clientProof []byte
	for body := third.body; len(body) >= 2; {
		tag, n := body[0], int(body[1])
		body = body[2:]
		if n > len(body) {
			return nil, io.ErrUnexpectedEOF
		}
		switch tag {
		case 3:
			clientPublic = append(clientPublic, body[:n]...)
		case 4:
			clientProof = append(clientProof, body[:n]...)
		}
		body = body[n:]
	}
	if len(clientPublic) == 0 || len(clientPublic) > len(prime.Bytes()) || len(clientProof) != sha512.Size {
		return nil, errors.New("测试接收端未收到有效的 HAP M3 公钥和证明")
	}
	client := new(big.Int).SetBytes(clientPublic)
	u := new(big.Int).SetBytes(fallbackSHA512(pad(client), pad(public)))
	base := new(big.Int).Mul(client, new(big.Int).Exp(verifier, u, prime))
	shared := new(big.Int).Exp(base, exponent, prime)
	secret := fallbackSHA512(shared.Bytes())
	hash := fallbackSHA512(prime.Bytes())
	for i, value := range fallbackSHA512(generator.Bytes()) {
		hash[i] ^= value
	}
	expected := fallbackSHA512(hash, fallbackSHA512([]byte("Pair-Setup")), salt, clientPublic, publicBytes, secret)
	if !bytes.Equal(expected, clientProof) {
		return nil, errors.New("测试接收端收到的 HAP M3 证明不匹配")
	}
	proof := fallbackSHA512(clientPublic, clientProof, secret)
	m4 := append([]byte{6, 1, 4, 4, byte(len(proof))}, proof...)
	return secret, writeFallbackReply(w, "HTTP/1.1", third.seq, m4)
}

func fallbackSHA512(parts ...[]byte) []byte {
	h := sha512.New()
	for _, part := range parts {
		h.Write(part)
	}
	return h.Sum(nil)
}

type fallbackRecords struct {
	cipher.AEAD
	reader  io.Reader
	writer  io.Writer
	seq     uint64
	pending []byte
}

func newFallbackRecords(secret []byte, label string) *fallbackRecords {
	key, _ := hkdf.Key(sha512.New, secret, []byte("Control-Salt"), label, chacha20poly1305.KeySize)
	aead, _ := chacha20poly1305.New(key)
	return &fallbackRecords{AEAD: aead}
}

func (r *fallbackRecords) nextNonce() []byte {
	var nonce [chacha20poly1305.NonceSize]byte
	binary.LittleEndian.PutUint64(nonce[4:], r.seq)
	r.seq++
	return nonce[:]
}

func (r *fallbackRecords) Read(p []byte) (int, error) {
	if len(r.pending) == 0 {
		var prefix [2]byte
		if _, err := io.ReadFull(r.reader, prefix[:]); err != nil {
			return 0, err
		}
		n := binary.LittleEndian.Uint16(prefix[:])
		if n == 0 || n > 1024 {
			return 0, fmt.Errorf("测试控制帧长度 = %d", n)
		}
		frame := make([]byte, int(n)+r.Overhead())
		if _, err := io.ReadFull(r.reader, frame); err != nil {
			return 0, err
		}
		plain, err := r.Open(frame[:0], r.nextNonce(), frame, prefix[:])
		if err != nil {
			return 0, err
		}
		r.pending = plain
	}
	n := copy(p, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

func (r *fallbackRecords) Write(p []byte) (int, error) {
	if len(p) > 1024 {
		return 0, fmt.Errorf("测试响应超出单帧长度：%d", len(p))
	}
	prefix := binary.LittleEndian.AppendUint16(nil, uint16(len(p)))
	if _, err := r.writer.Write(r.Seal(bytes.Clone(prefix), r.nextNonce(), p, prefix)); err != nil {
		return 0, err
	}
	return len(p), nil
}
