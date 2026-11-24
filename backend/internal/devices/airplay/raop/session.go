// Package raop 协商 AirPlay 1 的 ALAC、AES 和 UDP 端口。
package raop

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/netip"
	"strconv"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/devices/airplay/rtp"
	"github.com/qlqq/roomusic/backend/internal/devices/airplay/rtsp"
)

// AirPort 公开的 RSA 公钥参数用于 SDP 的密钥协商。分包 CBC 行为参考 libraop（MIT）的 src/raop_client.c。
const publicModulus = "59dE8qLieItsH1WgjrcFRKj6eUWqi+bGLOX1HL3U3GhC/j0Qg90u3sG/1CUtwC" +
	"5vOYvfDmFI6oSFXi5ELabWJmT2dKHzBJKa3k9ok+8t9ucRqMd6DZHJ2YCCLlDR" +
	"KSKv6kDqnw4UwPdpOMXziC/AMj3Z/lUVX1G7WSHCAWKf1zNS1eLvqr+boEjXuB" +
	"OitnZ/bDzPHrTOZz0Dew0uowxf/+sG+NCK3eQJVxqcaJ/vEHKIVd2M+5qL71yJ" +
	"Q+87X6oV3eaYvt3zWZYD6z5vYTcrtij2VZ9Zmni/UAaHqn9JdsBWLUEpVviYnhimNVvYFZeCXg/IdTQ+x4IRdiXNv5hEew=="

func keyExchange(methods string) (string, rtp.Encrypt, error) {
	if methods == "" {
		return "", nil, nil
	}
	supported := strings.Split(methods, ",")
	clearOK := false
	for _, method := range supported {
		switch strings.TrimSpace(method) {
		case "0":
			clearOK = true
		case "1":
			n, err := base64.StdEncoding.DecodeString(publicModulus)
			if err != nil {
				return "", nil, err
			}
			key, iv := make([]byte, 16), make([]byte, 16)
			rand.Read(key)
			rand.Read(iv)
			wrapped, err := rsa.EncryptOAEP(sha1.New(), rand.Reader, &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: 65537}, key, nil)
			if err != nil {
				return "", nil, err
			}
			block, err := aes.NewCipher(key)
			clear(key)
			if err != nil {
				return "", nil, err
			}
			attributes := fmt.Sprintf("a=rsaaeskey:%s\r\na=aesiv:%s\r\n", base64.RawStdEncoding.EncodeToString(wrapped), base64.RawStdEncoding.EncodeToString(iv))
			return attributes, func(_ []byte, p []byte, _ uint64) []byte {
				whole := len(p) / aes.BlockSize * aes.BlockSize
				cipher.NewCBCEncrypter(block, iv).CryptBlocks(p[:whole], p[:whole])
				return p
			}, nil
		}
	}
	if clearOK {
		return "", nil, nil
	}
	return "", nil, errors.New("RAOP 接收端没有可用的音频加密方式")
}

type Session struct{ URI, ID string }

func (s Session) Headers() http.Header {
	h := make(http.Header)
	if s.ID != "" {
		h.Set("Session", s.ID)
	}
	return h
}

func Setup(ctx context.Context, c *rtsp.Client, peer netip.Addr, methods string, t *rtp.Transport) (Session, error) {
	var s Session
	resp, err := c.Do(ctx, "OPTIONS", "*", nil, nil)
	if err != nil {
		return s, err
	}
	if err = resp.CheckStatus(); err != nil {
		return s, err
	}
	cryptoAttrs, encrypt, err := keyExchange(methods)
	if err != nil {
		return s, err
	}
	local := c.LocalAddr()
	_, stamp := t.Position()
	s.URI = fmt.Sprintf("rtsp://%s/%d", local, stamp)
	sdp := fmt.Sprintf("v=0\r\no=ROOMusic %d 0 IN IP4 %s\r\ns=ROOMusic\r\nc=IN IP4 %s\r\nt=0 0\r\nm=audio 0 RTP/AVP 96\r\na=rtpmap:96 AppleLossless\r\na=fmtp:96 352 0 16 40 10 14 2 255 0 0 44100\r\n%s", stamp, local, peer, cryptoAttrs)
	resp, err = c.Do(ctx, "ANNOUNCE", s.URI, http.Header{"Content-Type": {"application/sdp"}}, []byte(sdp))
	if err != nil {
		return s, err
	}
	if err = resp.CheckStatus(); err != nil {
		return s, err
	}
	ctl, ntp := t.Ports()
	transport := fmt.Sprintf("RTP/AVP/UDP;unicast;mode=record;control_port=%d;timing_port=%d", ctl, ntp)
	resp, err = c.Do(ctx, "SETUP", s.URI, http.Header{"Transport": {transport}}, nil)
	if err != nil {
		return s, err
	}
	if err = resp.CheckStatus(); err != nil {
		return s, err
	}
	s.ID, _, _ = strings.Cut(resp.Header.Get("Session"), ";")
	var audio, control uint16
	for part := range strings.SplitSeq(resp.Header.Get("Transport"), ";") {
		k, v, _ := strings.Cut(strings.TrimSpace(part), "=")
		if k != "server_port" && k != "control_port" {
			continue
		}
		port, e := strconv.ParseUint(v, 10, 16)
		if e != nil || port == 0 {
			return s, errors.New("RAOP SETUP 端口无效")
		}
		if k == "server_port" {
			audio = uint16(port)
		} else {
			control = uint16(port)
		}
	}
	if audio == 0 || control == 0 || s.ID == "" {
		return s, errors.New("RAOP SETUP 缺少会话或 RTP 端口")
	}
	t.Configure(audio, control, encrypt)
	seq, stamp := t.Position()
	h := s.Headers()
	h.Set("Range", "npt=0-")
	h.Set("RTP-Info", fmt.Sprintf("seq=%d;rtptime=%d", seq, stamp))
	resp, err = c.Do(ctx, "RECORD", s.URI, h, nil)
	if err != nil {
		return s, err
	}
	if err = resp.CheckStatus(); err != nil {
		return s, err
	}
	if raw := resp.Header.Get("Audio-Latency"); raw != "" {
		latency, e := strconv.ParseUint(raw, 10, 32)
		if e != nil || latency > 441000 {
			return s, errors.New("RAOP 接收端延迟无效")
		}
		// 接收端给出设备端最小延迟；实时发送再预留一秒的网络缓冲。行为参考 pyatv（MIT）。
		t.SetLatency(uint32(latency) + 44100)
	}
	return s, nil
}
