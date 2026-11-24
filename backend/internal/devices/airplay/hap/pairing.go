package hap

import (
	"bytes"
	"context"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/sha512"
	"errors"
	"fmt"
	"net/http"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/devices/airplay/rtsp"
	"golang.org/x/crypto/chacha20poly1305"
)

// 行为参考 pyatv（MIT）的消息序号、HKP 版本、签名绑定和密钥派生标签。
// https://github.com/postlund/pyatv/tree/master/pyatv/protocols/airplay/auth
// https://github.com/postlund/pyatv/blob/master/pyatv/auth/hap_srp.py
type pairing struct {
	client *rtsp.Client
	hkp    string
}

func (p pairing) post(ctx context.Context, path string, body []byte) ([]byte, error) {
	header := make(http.Header)
	header.Set("Content-Type", "application/octet-stream")
	header.Set("Connection", "keep-alive")
	header.Set("X-Apple-HKP", p.hkp)
	r, err := p.client.PostPairing(ctx, path, header, body)
	if err != nil {
		return nil, err
	}
	if r.StatusCode != 200 {
		return nil, fmt.Errorf("AirPlay %s: %w", path, &rtsp.StatusError{Code: r.StatusCode})
	}
	return r.Body, nil
}

func (p pairing) exchange(ctx context.Context, path string, state byte, fields ...field) (message, error) {
	body, err := p.post(ctx, path, encode(append([]field{{tagState, []byte{state}}}, fields...)...))
	if err != nil {
		return nil, err
	}
	m, err := decode(body)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(m[tagState], []byte{state + 1}) {
		return nil, fmt.Errorf("HAP %s 期望 M%d 响应", path, state+1)
	}
	if code, ok := m[tagError]; ok {
		if len(code) != 1 {
			return nil, errors.New("HAP 配对错误码无效")
		}
		return nil, fmt.Errorf("HAP %s M%d 接收端错误码 %d", path, state+1, code[0])
	}
	return m, nil
}

func validIdentifier(id []byte) bool {
	return len(id) > 0 && len(id) <= 64 && utf8.Valid(id) && bytes.IndexByte(id, 0) < 0
}

// Credentials 保存一次带 PIN 配对产生的长期身份。ControllerSeed 是 Ed25519 的 32 字节种子。
type Credentials struct {
	ControllerID       string
	ControllerSeed     []byte
	AccessoryID        string
	AccessoryPublicKey []byte
}

// Session 持有本次已认证连接的共享密钥材料。
type Session struct {
	secret []byte
}

// DeriveKey 用本次配对的共享密钥和指定通道的 HKDF 标签派生 32 字节密钥。
func (s *Session) DeriveKey(salt, info string) ([]byte, error) {
	return hkdf.Key(sha512.New, s.secret, []byte(salt), info, chacha20poly1305.KeySize)
}

func (s *Session) enable(client *rtsp.Client) (*Session, error) {
	writeKey, err := s.DeriveKey("Control-Salt", "Control-Write-Encryption-Key")
	if err != nil {
		return nil, err
	}
	readKey, err := s.DeriveKey("Control-Salt", "Control-Read-Encryption-Key")
	if err != nil {
		return nil, err
	}
	if err := client.EnableEncryption(writeKey, readKey); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Session) pairingKey(label string) ([]byte, error) {
	return s.DeriveKey(label+"-Salt", label+"-Info")
}

func (s *Session) pairingCipher(label string) (cipher.AEAD, error) {
	key, err := s.pairingKey(label)
	if err != nil {
		return nil, err
	}
	return chacha20poly1305.New(key)
}

func nonce(label string) []byte {
	var out [chacha20poly1305.NonceSize]byte
	copy(out[4:], label)
	return out[:]
}

func openMessage(aead cipher.AEAD, label string, ciphertext []byte) (message, error) {
	body, err := aead.Open(nil, nonce(label), ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("HAP %s 消息认证失败: %w", label, err)
	}
	return decode(body)
}

// AudioKey 取得实时流声明与 AEAD 共用的 32 字节密钥。
// 行为参考 airplay2-sender-cpp（Apache-2.0）：临时 SRP 与长期 ECDH 取相同宽度。
func (s *Session) AudioKey() []byte { return bytes.Clone(s.secret[:32]) }
