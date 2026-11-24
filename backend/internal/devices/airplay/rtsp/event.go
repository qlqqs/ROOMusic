package rtsp

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// ServeEvents 在独立的加密事件连接上读接收端请求并应答，关闭连接可结束等待。
// 行为参考 pyatv（MIT）的 EventChannel，响应保留请求的协议版本与 CSeq。
func (c *Client) ServeEvents() error {
	for {
		line, e := c.reader.ReadSlice('\n')
		if e != nil {
			return e
		}
		fields := strings.Fields(string(line))
		if len(fields) != 3 || (fields[2] != "RTSP/1.0" && fields[2] != "HTTP/1.1") {
			return errors.New("AirPlay 事件请求行无效")
		}
		version := fields[2]
		seq := ""
		size := uint64(0)
		total := len(line)
		for {
			line, e = c.reader.ReadSlice('\n')
			if e != nil {
				return e
			}
			total += len(line)
			if total > maxHeaders {
				return errors.New("AirPlay 事件请求头过大")
			}
			s := strings.TrimSpace(string(line))
			if s == "" {
				break
			}
			k, v, ok := strings.Cut(s, ":")
			if !ok {
				return errors.New("AirPlay 事件请求头无效")
			}
			switch strings.ToLower(k) {
			case "cseq":
				n, e := strconv.ParseUint(strings.TrimSpace(v), 10, 64)
				if e != nil {
					return e
				}
				seq = fmt.Sprintf("CSeq: %d\r\n", n)
			case "content-length":
				size, e = strconv.ParseUint(strings.TrimSpace(v), 10, 32)
				if e != nil || size > maxBody {
					return errors.New("AirPlay 事件请求体过大")
				}
			case "transfer-encoding":
				return errors.New("AirPlay 事件传输编码无效")
			}
		}
		if _, e = io.CopyN(io.Discard, c.reader, int64(size)); e != nil {
			return e
		}
		if _, e = fmt.Fprintf(c.writer, "%s 200 OK\r\n%sContent-Length: 0\r\nAudio-Latency: 0\r\n\r\n", version, seq); e != nil {
			return e
		}
	}
}
