package listening

import "math"

// 计一次播放的规则（服务端一侧）。前端的计次跟踪器用同一个公式：门槛 T = min(D/2, 240 秒)，D 是曲目时长。
const (
	// MaxThresholdSeconds 是门槛的上限：再长的曲目也只要累计出声 4 分钟。
	MaxThresholdSeconds = 240.0
	// ThresholdToleranceSeconds 吸收浮点误差与媒体事件的粒度：服务端接受 played_seconds ≥ T − 1。
	ThresholdToleranceSeconds = 1.0
	// MaxPlayedSeconds 是单条记录 played_seconds 的上限（24 小时），与表约束一致。
	MaxPlayedSeconds = 86400.0
)

// Threshold 返回计一次播放所需的实际出声秒数 T = min(D/2, 240)。D 是扫描得到的曲目时长；时长未知（不是正的有限数）时取 240。
func Threshold(durationSeconds float64) float64 {
	if !knownDuration(durationSeconds) {
		return MaxThresholdSeconds
	}
	return math.Min(durationSeconds/2, MaxThresholdSeconds)
}

// ValidPlayedSeconds 报告 played_seconds 是否在合理范围内：有限数，大于 0 且不超过 MaxPlayedSeconds。
func ValidPlayedSeconds(playedSeconds float64) bool {
	return !math.IsNaN(playedSeconds) && !math.IsInf(playedSeconds, 0) && playedSeconds > 0 && playedSeconds <= MaxPlayedSeconds
}

// meetsThreshold 在曲目时长已知时要求 playedSeconds ≥ T − 1；时长未知时没有可依据的门槛，只靠 ValidPlayedSeconds 约束数值范围。
func meetsThreshold(durationSeconds, playedSeconds float64) bool {
	if !knownDuration(durationSeconds) {
		return true
	}
	return playedSeconds >= Threshold(durationSeconds)-ThresholdToleranceSeconds
}

func knownDuration(durationSeconds float64) bool {
	return !math.IsNaN(durationSeconds) && !math.IsInf(durationSeconds, 0) && durationSeconds > 0
}
