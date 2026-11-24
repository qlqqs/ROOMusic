// Package castv2 实现 Google Cast 控制通道及其长度前缀消息。
package castv2

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"

	"google.golang.org/protobuf/encoding/protowire"
)

const (
	Connection = "urn:x-cast:com.google.cast.tp.connection"
	Heartbeat  = "urn:x-cast:com.google.cast.tp.heartbeat"
	Receiver   = "urn:x-cast:com.google.cast.receiver"
	Media      = "urn:x-cast:com.google.cast.media"
	ReceiverID = "receiver-0"
	DefaultApp = "CC1AD845"
	maxFrame   = 64 << 10
)

var errFrame = errors.New("无效的 Cast 消息帧")

type Message struct {
	Source, Destination, Namespace string
	Payload                        json.RawMessage
}

func Read(r io.Reader) (Message, error) {
	var m Message
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return m, err
	}
	size := binary.BigEndian.Uint32(header[:])
	if size == 0 || size > maxFrame {
		return m, errFrame
	}
	data := make([]byte, size)
	if _, err := io.ReadFull(r, data); err != nil {
		return m, err
	}
	var version, payloadType bool
	for len(data) > 0 {
		num, typ, n := protowire.ConsumeTag(data)
		if n < 0 {
			return m, errFrame
		}
		data = data[n:]
		switch num {
		case 1, 5:
			v, n := protowire.ConsumeVarint(data)
			if typ != protowire.VarintType || n < 0 || v != 0 {
				return m, errFrame
			}
			if num == 1 {
				version = true
			} else {
				payloadType = true
			}
			data = data[n:]
		case 2, 3, 4, 6:
			v, n := protowire.ConsumeBytes(data)
			if typ != protowire.BytesType || n < 0 {
				return m, errFrame
			}
			switch num {
			case 2:
				m.Source = string(v)
			case 3:
				m.Destination = string(v)
			case 4:
				m.Namespace = string(v)
			case 6:
				m.Payload = v
			}
			data = data[n:]
		default:
			n := protowire.ConsumeFieldValue(num, typ, data)
			if n < 0 {
				return m, errFrame
			}
			data = data[n:]
		}
	}
	if !version || !payloadType || m.Source == "" || m.Destination == "" || m.Namespace == "" || !json.Valid(m.Payload) {
		return m, errFrame
	}
	return m, nil
}

func Write(w io.Writer, m Message) error {
	if m.Source == "" || m.Destination == "" || m.Namespace == "" || len(m.Payload) > maxFrame || !json.Valid(m.Payload) {
		return errFrame
	}
	data := make([]byte, 4, 128+len(m.Payload))
	enum := func(n protowire.Number) {
		data = protowire.AppendTag(data, n, protowire.VarintType)
		data = protowire.AppendVarint(data, 0)
	}
	field := func(n protowire.Number, v string) {
		data = protowire.AppendTag(data, n, protowire.BytesType)
		data = protowire.AppendString(data, v)
	}
	enum(1)
	field(2, m.Source)
	field(3, m.Destination)
	field(4, m.Namespace)
	enum(5)
	field(6, string(m.Payload))
	if len(data)-4 > maxFrame {
		return errFrame
	}
	binary.BigEndian.PutUint32(data[:4], uint32(len(data)-4))
	n, err := w.Write(data)
	if n != len(data) && err == nil {
		return io.ErrShortWrite
	}
	return err
}
