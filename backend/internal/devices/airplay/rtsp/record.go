package rtsp

import (
	"bufio"
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"io"
	"math"

	"golang.org/x/crypto/chacha20poly1305"
)

// HAP 控制帧以两字节小端明文长度为关联数据，双向 nonce 分别从零计数。
// 行为参考 pyatv 的 HAPSession（MIT）。
const recordLimit = 1024

type recordCipher struct {
	cipher.AEAD
	counter uint64
}

func (c *recordCipher) nonce() ([]byte, error) {
	if c.counter == math.MaxUint64 {
		return nil, errors.New("HAP 帧计数已耗尽")
	}
	var nonce [chacha20poly1305.NonceSize]byte
	binary.LittleEndian.PutUint64(nonce[4:], c.counter)
	c.counter++
	return nonce[:], nil
}

type recordWriter struct {
	recordCipher
	w io.Writer
}

func (w *recordWriter) Write(p []byte) (int, error) {
	total := 0
	for len(p) > 0 {
		n := min(len(p), recordLimit)
		nonce, err := w.nonce()
		if err != nil {
			return total, err
		}
		var frame [2 + recordLimit + chacha20poly1305.Overhead]byte
		binary.LittleEndian.PutUint16(frame[:2], uint16(n))
		out := w.Seal(frame[:2], nonce, p[:n], frame[:2])
		nw, err := w.w.Write(out)
		if err != nil {
			return total, err
		}
		if nw != len(out) {
			return total, io.ErrShortWrite
		}
		total += n
		p = p[n:]
	}
	return total, nil
}

type recordReader struct {
	recordCipher
	r       io.Reader
	pending []byte
}

func (r *recordReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for len(r.pending) == 0 {
		var prefix [2]byte
		if _, err := io.ReadFull(r.r, prefix[:]); err != nil {
			return 0, err
		}
		size := int(binary.LittleEndian.Uint16(prefix[:]))
		if size > recordLimit {
			return 0, errors.New("HAP 控制帧过大")
		}
		frame := make([]byte, size+r.Overhead())
		if _, err := io.ReadFull(r.r, frame); err != nil {
			return 0, err
		}
		nonce, err := r.nonce()
		if err != nil {
			return 0, err
		}
		r.pending, err = r.Open(frame[:0], nonce, frame, prefix[:])
		if err != nil {
			return 0, err
		}
	}
	n := copy(p, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

func (c *Client) EnableEncryption(writeKey, readKey []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.encrypted {
		return errors.New("RTSP 控制连接已启用加密")
	}
	w, err := chacha20poly1305.New(writeKey)
	if err != nil {
		return err
	}
	r, err := chacha20poly1305.New(readKey)
	if err != nil {
		return err
	}
	// 明文响应后的首个密文帧可能已预读到原缓冲区。
	c.reader = bufio.NewReader(&recordReader{recordCipher: recordCipher{AEAD: r}, r: c.reader})
	c.writer = &recordWriter{recordCipher: recordCipher{AEAD: w}, w: c.conn}
	c.encrypted = true
	return nil
}
