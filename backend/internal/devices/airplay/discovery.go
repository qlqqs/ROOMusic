// Package airplay 发现并控制局域网 AirPlay 接收端。
package airplay

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/devices/lan"
	"github.com/qlqq/roomusic/backend/internal/devices/mdns"
)

const (
	airplayService = "_airplay._tcp.local."
	raopService    = "_raop._tcp.local."
)

var ErrAddress = errors.New("需要局域网 IPv4 地址")

// nullableText 在设备能力中保留字段，空文本以 JSON null 表示。
type nullableText string

func (s nullableText) MarshalJSON() ([]byte, error) {
	if s == "" {
		return []byte("null"), nil
	}
	return json.Marshal(string(s))
}

type Info struct {
	DeviceID          string       `json:"device_id"`
	AirPlayAddress    string       `json:"airplay_address,omitempty"`
	RAOPAddress       string       `json:"raop_address,omitempty"`
	Features          uint64       `json:"features,string"`
	Flags             uint64       `json:"flags,string"`
	PublicKey         string       `json:"public_key,omitempty"`
	Paired            bool         `json:"paired"`
	RequiresPIN       bool         `json:"requires_pin"`
	RequiresPassword  bool         `json:"requires_password"`
	RAOPEncryption    string       `json:"raop_encryption,omitempty"`
	PTPRequired       bool         `json:"ptp_required"`
	TimingProtocol    nullableText `json:"timing_protocol"`
	TimingReason      nullableText `json:"timing_reason"`
	FairPlay          bool         `json:"fairplay"`
	Unsupported       bool         `json:"unsupported"`
	UnavailableReason nullableText `json:"unavailable_reason"`
}

type Device struct {
	Key, Name, Model, Address, LocalAddress string
	Info
}

func deviceID(raw string) string {
	if len(raw) == 12 {
		if decoded, err := hex.DecodeString(raw); err == nil {
			return strings.ToUpper(net.HardwareAddr(decoded).String())
		}
	}
	if mac, err := net.ParseMAC(raw); err == nil && len(mac) == 6 {
		return strings.ToUpper(mac.String())
	}
	return ""
}

// DNS-SD 按低位字、高位字的顺序公布特性，两个字分别占 32 位。
// 协议字段见 https://openairplay.github.io/airplay-spec/service_discovery.html 。
func bitfield(raw string) (uint64, bool) {
	if raw == "" {
		return 0, true
	}
	parse := func(word string, bits int) (uint64, error) {
		word = strings.TrimSpace(word)
		return strconv.ParseUint(strings.TrimPrefix(strings.TrimPrefix(word, "0x"), "0X"), 16, bits)
	}
	low, high, both := strings.Cut(raw, ",")
	if !both {
		n, err := parse(low, 64)
		return n, err == nil
	}
	l, err := parse(low, 32)
	if err != nil {
		return 0, false
	}
	h, err := parse(high, 32)
	return l | h<<32, err == nil
}

func parseEntry(e mdns.Entry) (Device, bool) {
	if !lan.IsLANAddress(e.IP) || e.Port == 0 {
		return Device{}, false
	}
	id := deviceID(e.TXT["deviceid"])
	name, model, features, flags := e.Instance, e.TXT["model"], e.TXT["features"], e.TXT["flags"]
	addr := netip.AddrPortFrom(e.IP, e.Port).String()
	var info Info
	switch e.Service {
	case airplayService:
		info.AirPlayAddress = addr
	case raopService:
		prefix, suffix, found := strings.Cut(name, "@")
		if found {
			raopID := deviceID(prefix)
			if raopID == "" || (id != "" && id != raopID) {
				return Device{}, false
			}
			id, name = raopID, suffix
		}
		info.RAOPAddress = addr
		info.RAOPEncryption = e.TXT["et"]
		if model == "" {
			model = e.TXT["am"]
		}
		if features == "" {
			features = e.TXT["ft"]
		}
		if flags == "" {
			flags = e.TXT["sf"]
		}
	default:
		return Device{}, false
	}
	name = strings.TrimSpace(name)
	if id == "" || name == "" || !utf8.ValidString(name) || !utf8.ValidString(model) ||
		strings.IndexFunc(name, unicode.IsControl) >= 0 || strings.IndexFunc(model, unicode.IsControl) >= 0 {
		return Device{}, false
	}
	var ok bool
	if info.Features, ok = bitfield(features); !ok {
		return Device{}, false
	}
	if info.Flags, ok = bitfield(flags); !ok {
		return Device{}, false
	}
	if raw := e.TXT["pk"]; raw != "" {
		pk, err := hex.DecodeString(raw)
		if err != nil || len(pk) != ed25519.PublicKeySize {
			return Device{}, false
		}
		info.PublicKey = strings.ToLower(raw)
	}
	info.DeviceID = id
	// PIN 与密码的标志位不同。行为参考 pyatv（MIT）的 protocols/airplay/utils.py
	// 以及 https://openairplay.github.io/airplay-spec/status_flags.html 。
	info.RequiresPIN = info.Flags&(1<<3|1<<9) != 0 || strings.HasPrefix(model, "AppleTV")
	info.RequiresPassword = info.Flags&(1<<7) != 0 || strings.EqualFold(e.TXT["pw"], "true")
	info.authentication()
	return Device{Key: "airplay:" + id, Name: name, Model: model, Address: addr, LocalAddress: e.LocalIP.String(), Info: info}, true
}

// 认证特性表示接收端支持的方式。行为参考 shairport-sync 5.6.3 的 DNS-SD 公告，同时声明 FairPlay 和 transient。
// 只有缺少可用的替代认证时才标记不可用。见 Unofficial AirPlay Specification 的 Features 表。
func (i *Info) authentication() {
	i.FairPlay = i.Features&(1<<12|1<<14|1<<51) != 0
	i.UnavailableReason = i.authenticationReason()
	i.Unsupported = i.UnavailableReason != ""
}

func (i Info) authenticationReason() nullableText {
	if i.AirPlayAddress == "" && (i.RequiresPassword || i.RequiresPIN) {
		return "此设备只提供需要认证的 RAOP 服务，无法使用屏幕 PIN 配对"
	}
	if !i.FairPlay || i.Features&(1<<43|1<<46|1<<48) != 0 {
		return ""
	}
	for _, method := range strings.Split(i.RAOPEncryption, ",") {
		switch strings.TrimSpace(method) {
		case "0", "1":
			return ""
		}
	}
	return "设备需要 FairPlay 认证"
}

// 两种公告可给出不同的端口；发送端的本机地址随首选服务一同选取。
func merge(native, classic Device) Device {
	if native.Key == "" {
		return classic
	}
	if classic.Key == "" {
		return native
	}
	native.RAOPAddress = classic.RAOPAddress
	if native.Model == "" {
		native.Model = classic.Model
	}
	if native.PublicKey == "" {
		native.PublicKey = classic.PublicKey
	}
	native.Features |= classic.Features
	native.Flags |= classic.Flags
	native.RequiresPIN = native.RequiresPIN || classic.RequiresPIN
	native.RequiresPassword = native.RequiresPassword || classic.RequiresPassword
	native.RAOPEncryption = classic.RAOPEncryption
	native.Info.authentication()
	return native
}

func Probe(ctx context.Context, address string) (Device, error) {
	ip, err := netip.ParseAddr(address)
	if err != nil || !lan.IsLANAddress(ip) {
		return Device{}, ErrAddress
	}
	return lookup(ctx, ip, "")
}

func lookup(ctx context.Context, ip netip.Addr, key string) (Device, error) {
	var native, classic Device
	var failures error
	for _, service := range []string{airplayService, raopService} {
		entry, err := mdns.LookupMatch(ctx, service, ip, func(e mdns.Entry) bool {
			d, ok := parseEntry(e)
			return ok && (key == "" || key == d.Key)
		})
		if ctx.Err() != nil {
			return Device{}, ctx.Err()
		}
		if err != nil {
			failures = errors.Join(failures, err)
			continue
		}
		d, _ := parseEntry(entry)
		key = d.Key
		if service == airplayService {
			native = d
		} else {
			classic = d
		}
	}
	result := merge(native, classic)
	if result.Key == "" {
		return Device{}, failures
	}
	return result, nil
}
