// Package schema 定义匿名心跳的线上格式与分档。
package schema

import (
	"errors"
	"slices"

	"github.com/qlqq/roomusic/backend/internal/platform/buildinfo"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

const (
	Version       = 2
	DevVersion    = "dev"
	MaxBytes      = 4 << 10
	Zero          = "0"
	One           = "1"
	TwoToFive     = "2-5"
	SixPlus       = "6+"
	OneTo999      = "1-999"
	OneTo10k      = "1k-10k"
	TenTo50k      = "10k-50k"
	FiftyKPlus    = "50k+"
	SixTo20       = "6-20"
	TwentyOnePlus = "21+"
)

type Payload struct {
	Schema     int      `json:"schema"`
	InstanceID string   `json:"instance_id"`
	Version    string   `json:"version"`
	OS         string   `json:"os"`
	Arch       string   `json:"arch"`
	Container  bool     `json:"container"`
	Library    Library  `json:"library"`
	Members    string   `json:"members"`
	Features   Features `json:"features"`
	Devices30d Devices  `json:"devices_30d"`
}

type Library struct {
	Tracks   string `json:"tracks"`
	Releases string `json:"releases"`
}

type Features struct {
	AutoCuration  bool   `json:"auto_curation"`
	StewardModel  bool   `json:"steward_model"`
	MCPToken      bool   `json:"mcp_token"`
	LANDevices    bool   `json:"lan_devices"`
	DSP           bool   `json:"dsp"`
	ScheduledScan bool   `json:"scheduled_scan"`
	Plugins       string `json:"plugins"`
}

type Devices struct {
	Browser string `json:"browser"`
	UPnP    string `json:"upnp"`
	Cast    string `json:"cast,omitempty"`
}

func LibraryBucket(n int) string {
	switch {
	case n == 0:
		return Zero
	case n < 1000:
		return OneTo999
	case n < 10000:
		return OneTo10k
	case n < 50000:
		return TenTo50k
	default:
		return FiftyKPlus
	}
}

func MembersBucket(n int) string {
	switch {
	case n == 1:
		return One
	case n >= 2 && n <= 5:
		return TwoToFive
	case n >= 6 && n <= 20:
		return SixTo20
	case n >= 21:
		return TwentyOnePlus
	default:
		return ""
	}
}

func DeviceBucket(n int) string {
	switch {
	case n == 0:
		return Zero
	case n == 1:
		return One
	case n <= 5:
		return TwoToFive
	default:
		return SixPlus
	}
}

var ErrInvalid = errors.New("心跳格式无效")

func (p Payload) Validate() error {
	library := []string{Zero, OneTo999, OneTo10k, TenTo50k, FiftyKPlus}
	small := []string{Zero, One, TwoToFive, SixPlus}
	switch p.Schema {
	case 1:
		if p.Devices30d.Cast != "" {
			return ErrInvalid
		}
	case 2:
		if !slices.Contains(small, p.Devices30d.Cast) {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	if !uuid.Valid(p.InstanceID) ||
		len(p.Version) > 64 || (p.Version != DevVersion && !buildinfo.ValidVersion(p.Version)) ||
		p.OS != "linux" || (p.Arch != "amd64" && p.Arch != "arm64") ||
		!slices.Contains(library, p.Library.Tracks) || !slices.Contains(library, p.Library.Releases) ||
		!slices.Contains([]string{One, TwoToFive, SixTo20, TwentyOnePlus}, p.Members) ||
		!slices.Contains(small, p.Features.Plugins) || !slices.Contains(small, p.Devices30d.Browser) || !slices.Contains(small, p.Devices30d.UPnP) {
		return ErrInvalid
	}
	return nil
}
