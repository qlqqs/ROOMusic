package ptp

import (
	"encoding/binary"
	"math"
	"time"
)

// IEEE 1588 v2 UDP 报文，字段参考 facebook/time 的 ptp/protocol（Apache-2.0）。
const (
	msgSync           = 0
	msgDelayReq       = 1
	msgPdelayReq      = 2
	msgPdelayResp     = 3
	msgFollowUp       = 8
	msgDelayResp      = 9
	msgPdelayFollowUp = 10
	msgAnnounce       = 11
	msgSignaling      = 12

	twoStep = 0x200
	unicast = 0x400
)

var be = binary.BigEndian

type portID struct {
	clock uint64
	port  uint16
}

func readID(b []byte) portID { return portID{be.Uint64(b), be.Uint16(b[8:])} }
func (id portID) put(b []byte) {
	be.PutUint64(b, id.clock)
	be.PutUint16(b[8:], id.port)
}

func message(id portID, kind byte, length int, sequence uint16, flags uint16, interval int8) []byte {
	b := make([]byte, length)
	b[0], b[1] = kind, 2
	be.PutUint16(b[2:], uint16(length))
	be.PutUint16(b[6:], flags)
	id.put(b[20:])
	be.PutUint16(b[30:], sequence)
	b[32] = 5
	switch kind {
	case msgSync:
		b[32] = 0
	case msgDelayReq:
		b[32] = 1
	case msgFollowUp:
		b[32] = 2
	case msgDelayResp:
		b[32] = 3
	}
	b[33] = byte(interval)
	return b
}

func stamp(b []byte, ns int64) {
	sec := uint64(ns / int64(time.Second))
	be.PutUint16(b, uint16(sec>>32))
	be.PutUint32(b[2:], uint32(sec))
	be.PutUint32(b[6:], uint32(ns%int64(time.Second)))
}

func readStamp(b []byte) (int64, bool) {
	sec := uint64(be.Uint16(b))<<32 | uint64(be.Uint32(b[2:]))
	nsec := uint64(be.Uint32(b[6:]))
	if nsec >= uint64(time.Second) || sec > (math.MaxInt64-nsec)/uint64(time.Second) {
		return 0, false
	}
	return int64(sec*uint64(time.Second) + nsec), true
}

type packet struct {
	b          []byte
	kind       byte
	source     portID
	seq, flags uint16
	correction int64
}

func parse(b []byte, event bool) (packet, bool) {
	if len(b) < 34 || b[1]&15 != 2 || b[4] != 0 {
		return packet{}, false
	}
	kind := b[0] & 15
	n := int(be.Uint16(b[2:]))
	var size int
	switch kind {
	case msgSync, msgDelayReq, msgFollowUp:
		size = 44
	case msgPdelayReq, msgPdelayResp, msgDelayResp, msgPdelayFollowUp:
		size = 54
	case msgAnnounce:
		size = 64
	case msgSignaling:
		size = 48
	default:
		return packet{}, false
	}
	if n < size || n > len(b) || event != (kind < 8) {
		return packet{}, false
	}
	id := readID(b[20:])
	if id.clock == 0 || id.port == 0 {
		return packet{}, false
	}
	return packet{b[:n], kind, id, be.Uint16(b[30:]), be.Uint16(b[6:]), int64(be.Uint64(b[8:])) / 65536}, true
}

func announce(id portID, seq uint16, flags uint16) []byte {
	b := message(id, msgAnnounce, 64, seq, flags, 0)
	// 自由运行的系统时钟，使用 ARB 时间尺度，不声明 UTC/TAI 可追溯性。
	b[47], b[48], b[49] = 248, 248, 0xfe
	be.PutUint16(b[50:], 0xffff)
	b[52] = 128
	be.PutUint64(b[53:], id.clock)
	b[63] = 0xa0
	return b
}

func subscription(id, target portID, seq uint16, kind byte, interval int8) []byte {
	b := message(id, msgSignaling, 54, seq, unicast, 0x7f)
	target.put(b[34:])
	be.PutUint16(b[44:], 4)
	be.PutUint16(b[46:], 6)
	b[48], b[49] = kind<<4, byte(interval)
	be.PutUint32(b[50:], 60)
	return b
}
