package airplay

import "github.com/qlqq/roomusic/backend/internal/ptp"

const PrecisionUnavailable = "精确对时不可用"

func RequiresPTP(model string) bool {
	return ptp.ReceiverProfile(model).Required
}

func (i Info) PrefersPTP(model string) bool {
	return i.AirPlayAddress != "" && (RequiresPTP(model) || i.Features&(1<<41) != 0)
}

// 时钟可用性只在生成设备快照时计算；配对及认证信息来自设备公告。
func (i *Info) ApplyTiming(model string, clock func() ptp.Status) {
	i.PTPRequired = RequiresPTP(model)
	i.TimingProtocol = "NTP"
	i.TimingReason = ""
	i.authentication()
	if i.PTPRequired && i.AirPlayAddress == "" {
		i.TimingProtocol = ""
		i.TimingReason = "missing_airplay_service"
		i.Unsupported = true
		i.UnavailableReason = PrecisionUnavailable
		return
	}
	if !i.PrefersPTP(model) {
		return
	}
	status := clock()
	if status.CanNegotiate() {
		i.TimingProtocol = "PTP"
		return
	}
	i.TimingReason = nullableText(status.Reason)
	if i.TimingReason == "" {
		i.TimingReason = "clock_unavailable"
	}
	if i.PTPRequired {
		i.Unsupported = true
		i.UnavailableReason = PrecisionUnavailable
		i.TimingProtocol = ""
	}
}
