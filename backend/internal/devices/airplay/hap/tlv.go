// Package hap 实现 AirPlay 控制端的临时配对、屏幕 PIN 配对和长期身份验证。
package hap

import (
	"bytes"
	"errors"
)

const (
	tagMethod byte = iota
	tagIdentifier
	tagSalt
	tagPublicKey
	tagProof
	tagEncrypted
	tagState
	tagError
)

const tagSignature = 0x0a
const tagFlags = 0x13

type field struct {
	tag  byte
	data []byte
}

// HAP 的单字段至多占 255 字节。较长的公钥用连续同类型字段传输。
func encode(fields ...field) []byte {
	var out []byte
	for _, f := range fields {
		for len(f.data) > 255 {
			out = append(out, f.tag, 255)
			out = append(out, f.data[:255]...)
			f.data = f.data[255:]
		}
		out = append(out, f.tag, byte(len(f.data)))
		out = append(out, f.data...)
	}
	return out
}

type message map[byte][]byte

func decode(data []byte) (message, error) {
	fields := make(message)
	var previous byte
	var previousSize int
	for len(data) > 0 {
		if len(data) < 2 || int(data[1]) > len(data)-2 {
			return nil, errors.New("HAP TLV8 字段截断")
		}
		key, size := data[0], int(data[1])
		if _, repeated := fields[key]; repeated && (previous != key || previousSize != 255) {
			return nil, errors.New("HAP TLV8 字段重复")
		}
		fields[key] = append(fields[key], data[2:2+size]...)
		data = data[2+size:]
		previous, previousSize = key, size
	}
	return fields, nil
}

func joined(parts ...[]byte) []byte {
	return bytes.Join(parts, nil)
}
