// Package rtsp 实现 AirPlay 明文与配对后的 RTSP 控制连接。
package rtsp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/textproto"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/qlqq/roomusic/backend/internal/devices/lan"
	"golang.org/x/net/http/httpguts"
)

// 行为参考 OwnTone issue 2042，新版接收端会核对发送端版本。
const UserAgent = "AirPlay/999.0.0"

const (
	requestTimeout = 10 * time.Second
	maxBody        = 1 << 20
	maxHeaders     = 64 << 10
	maxExpired     = 4
)

type Response struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

type StatusError struct {
	Code int
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("RTSP 请求返回 %d", e.Code)
}

func (r *Response) CheckStatus() error {
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		return &StatusError{Code: r.StatusCode}
	}
	return nil
}

type wireReader struct {
	io.Reader
	received bool
}

func (r *wireReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if n > 0 {
		r.received = true
	}
	return n, err
}

type Client struct {
	conn         net.Conn
	mu           sync.Mutex
	wire         wireReader
	plainReader  bufio.Reader
	reader       *bufio.Reader
	writer       io.Writer
	seq          uint64
	expired      map[string]string // 过期 CSeq 对应的请求协议。
	encrypted    bool
	instance     string
	activeRemote uint32
}

func Dial(ctx context.Context, address string) (*Client, error) {
	addr, err := netip.ParseAddrPort(address)
	if err != nil || !lan.IsLANAddress(addr.Addr()) || addr.Port() == 0 {
		return nil, errors.New("RTSP 需要局域网 IPv4 地址和端口")
	}
	dialer := net.Dialer{Timeout: requestTimeout}
	conn, err := dialer.DialContext(ctx, "tcp4", addr.String())
	if err != nil {
		return nil, err
	}
	// 行为参考 airplay2-sender-cpp（Apache-2.0），部分接收端在配对时就核对会话身份头。
	var identity [12]byte
	rand.Read(identity[:])
	c := &Client{conn: conn, writer: conn, instance: fmt.Sprintf("%X", identity[:8]), activeRemote: binary.BigEndian.Uint32(identity[8:])}
	c.wire.Reader = conn
	c.plainReader.Reset(&c.wire)
	c.reader = &c.plainReader
	return c, nil
}

func (c *Client) Close() error { return c.conn.Close() }

func (c *Client) LocalAddr() netip.Addr {
	return c.conn.LocalAddr().(*net.TCPAddr).AddrPort().Addr().Unmap()
}

func (c *Client) Do(ctx context.Context, method, target string, header http.Header, body []byte) (*Response, error) {
	return c.request(ctx, method, target, "RTSP/1.0", header, body)
}

// PostPairing 在控制连接上以 HTTP/1.1 交换 HAP 消息。部分接收端仍返回 RTSP 状态行。
func (c *Client) PostPairing(ctx context.Context, target string, header http.Header, body []byte) (*Response, error) {
	return c.request(ctx, "POST", target, "HTTP/1.1", header, body)
}

func (c *Client) request(ctx context.Context, method, target, protocol string, header http.Header, body []byte) (*Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !httpguts.ValidHeaderFieldName(method) || target == "" || strings.IndexFunc(target, func(r rune) bool { return r <= 0x20 || r == 0x7f }) >= 0 {
		return nil, errors.New("无效的 RTSP 方法或目标")
	}
	var req bytes.Buffer
	seq := c.seq + 1
	fmt.Fprintf(&req, "%s %s %s\r\nCSeq: %d\r\nUser-Agent: %s\r\n", method, target, protocol, seq, UserAgent)
	if protocol == "HTTP/1.1" {
		fmt.Fprintf(&req, "Host: %s\r\n", c.conn.RemoteAddr())
	}
	fmt.Fprintf(&req, "DACP-ID: %s\r\nClient-Instance: %s\r\nActive-Remote: %d\r\nX-Apple-Client-Name: ROOMusic\r\n", c.instance, c.instance, c.activeRemote)
	for k, vs := range header {
		switch strings.ToLower(k) {
		case "cseq", "user-agent", "content-length", "transfer-encoding", "dacp-id", "client-instance", "active-remote", "x-apple-client-name":
			return nil, fmt.Errorf("RTSP 客户端负责请求头 %s", k)
		}
		if !httpguts.ValidHeaderFieldName(k) {
			return nil, errors.New("无效的 RTSP 请求头名称")
		}
		for _, v := range vs {
			if !httpguts.ValidHeaderFieldValue(v) {
				return nil, errors.New("无效的 RTSP 请求头值")
			}
			fmt.Fprintf(&req, "%s: %s\r\n", k, v)
		}
	}
	if body != nil || method == "POST" {
		fmt.Fprintf(&req, "Content-Length: %d\r\n", len(body))
	}
	req.WriteString("\r\n")
	req.Write(body)
	deadline := time.Now().Add(requestTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := c.conn.SetDeadline(deadline); err != nil {
		return nil, err
	}
	canceled := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { c.conn.SetDeadline(time.Now()); close(canceled) })
	defer func() {
		if !stop() {
			<-canceled
		}
	}()
	c.seq = seq
	_, err := req.WriteTo(c.writer)
	keepOpen := false
	if err == nil {
		// 配对前的缓冲区可能预读了密文，解出首个明文字节前也可能只收到半帧。
		c.wire.received = c.plainReader.Buffered() > 0
		current := strconv.FormatUint(seq, 10)
		if _, err = c.reader.Peek(1); err == nil {
			var resp *Response
			resp, err = c.readCurrentResponse(current, protocol)
			if err == nil {
				return resp, nil
			}
		} else if errors.Is(err, os.ErrDeadlineExceeded) && ctx.Err() != context.Canceled &&
			!c.wire.received && len(c.expired) < maxExpired && (!c.encrypted || c.wire.Reader != nil) {
			if c.expired == nil {
				c.expired = make(map[string]string)
			}
			c.expired[current] = protocol
			keepOpen = true
		}
	}
	if !keepOpen {
		c.conn.Close()
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return nil, err
}

func (c *Client) readCurrentResponse(seq, protocol string) (*Response, error) {
	for {
		resp, err := readResponse(c.reader, seq, protocol, c.expired)
		if err != nil {
			return nil, err
		}
		replySeq := resp.Header.Get("CSeq")
		if _, stale := c.expired[replySeq]; !stale {
			return resp, nil
		}
		delete(c.expired, replySeq)
	}
}

func readResponse(r *bufio.Reader, seq, protocol string, expired map[string]string) (*Response, error) {
	line, err := r.ReadSlice('\n')
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(string(line))
	if len(fields) < 2 || len(fields[1]) != 3 {
		return nil, errors.New("无效的 RTSP 状态行")
	}
	expiredHTTP := false
	if fields[0] != "RTSP/1.0" {
		for _, p := range expired {
			if p == "HTTP/1.1" {
				expiredHTTP = true
				break
			}
		}
		if fields[0] != "HTTP/1.1" || (protocol != fields[0] && !expiredHTTP) {
			return nil, errors.New("无效的 RTSP 状态行")
		}
	}
	status, err := strconv.Atoi(fields[1])
	if err != nil || status < 100 || status > 599 {
		return nil, errors.New("无效的 RTSP 状态码")
	}
	h := make(http.Header)
	total := len(line)
	for {
		line, err = r.ReadSlice('\n')
		if err != nil {
			return nil, err
		}
		total += len(line)
		if total > maxHeaders {
			return nil, errors.New("RTSP 响应头过大")
		}
		s := strings.TrimSuffix(strings.TrimSuffix(string(line), "\n"), "\r")
		if s == "" {
			break
		}
		key, value, ok := strings.Cut(s, ":")
		if !ok || !httpguts.ValidHeaderFieldName(key) || !httpguts.ValidHeaderFieldValue(value) {
			return nil, errors.New("无效的 RTSP 响应头")
		}
		h.Add(key, textproto.TrimString(value))
	}
	replySeq := h.Get("CSeq")
	if replySeq != "" || fields[0] == "RTSP/1.0" || expiredHTTP {
		replyProtocol, stale := expired[replySeq]
		if len(h.Values("CSeq")) != 1 || (!stale && replySeq != seq) {
			return nil, errors.New("RTSP 响应序号不匹配")
		}
		if stale {
			protocol = replyProtocol
		}
		if fields[0] != "RTSP/1.0" && fields[0] != protocol {
			return nil, errors.New("无效的 RTSP 状态行")
		}
	}
	resp := &Response{StatusCode: status, Header: h}
	if len(h.Values("Transfer-Encoding")) != 0 {
		return nil, errors.New("RTSP 响应包含不支持的传输编码")
	}
	if lengths := h.Values("Content-Length"); len(lengths) > 0 {
		n, err := strconv.ParseUint(lengths[0], 10, 32)
		if err != nil || len(lengths) != 1 || n > maxBody {
			return nil, errors.New("无效的 RTSP 响应体长度")
		}
		resp.Body = make([]byte, int(n))
		if _, err = io.ReadFull(r, resp.Body); err != nil {
			return nil, err
		}
	}
	return resp, nil
}
