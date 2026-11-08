// Package playback 拥有“把已入库曲目交给浏览器播放”的用例：哪些（来源类别、编码）组合获准交付、
// 播放时重新核对来源（目录仍启用、文件在目录内且仍是扫描时的那个）、安全地只读打开，以及已建立传输的周期复核。
// 浏览器能直接解码的格式直出原文件（M2a）；ALAC 等不能直接播放的无损格式经 playback/derivation 生成经过核对的 FLAC 派生文件再交付（M2b）。
// 它不含 HTTP；传输层在 playback/httpapi。播放从不写音乐目录，也不转成有损格式。
package playback

import "github.com/qlqq/roomusic/backend/internal/playback/derivation"

// container 是格式表中的组合期望的文件容器，只用于核对文件头。
type container int

const (
	containerFLAC container = iota + 1
	containerMP4
	containerMP3
	containerWAV
	containerOgg
	containerAPE
	containerWavPack
	containerAIFF
)

// Delivery 是格式交给浏览器的方式。
type Delivery string

const (
	// DeliveryOriginal 直出原文件。
	DeliveryOriginal Delivery = "original"
	// DeliveryDerived 交付服务端无损转换并核对过的派生文件：浏览器拿到的是 FLAC。
	DeliveryDerived Delivery = "derived"
)

// Format 是一个获准交付的（来源类别、编码）组合，以及交给浏览器的表示的媒体类型。
// MIMEType 与 Codecs 描述浏览器拿到的表示，而不是源文件：直出时是原文件的类型，派生时是转换后的类型。
// Codecs 是 `codecs=` 参数的值，空表示没有该参数；前端用 MIMEType 与 Codecs 询问浏览器能否解码。
type Format struct {
	SourceKind string
	Codec      string
	Delivery   Delivery
	MIMEType   string
	Codecs     string
	// container 是源文件期望的容器，只用于核对文件头。
	container container
	input     derivation.Input
	policy    string
	decoder   string
}

// formatTable 是直出格式表，只保留拿到验证证据的组合（见 M2a 设计的格式表）：表中格式都经真实 Chromium 自动化用合成样本验证。
// 不在表中的组合一律不直出：m4a/alac（只能经派生，见 derivedFormatTable）、cue_virtual（需要精确切分，另行规划）、
// ieee_float、wav_extensible 与其他 WAV 编码、未知组合。
var formatTable = []Format{
	{SourceKind: "flac_vorbis_comment", Codec: "flac", Delivery: DeliveryOriginal, MIMEType: "audio/flac", container: containerFLAC},
	{SourceKind: "mp3_id3v2", Codec: "mp3", Delivery: DeliveryOriginal, MIMEType: "audio/mpeg", container: containerMP3},
	{SourceKind: "wav_info", Codec: "pcm", Delivery: DeliveryOriginal, MIMEType: "audio/wav", Codecs: "1", container: containerWAV},
	{SourceKind: "m4a", Codec: "aac", Delivery: DeliveryOriginal, MIMEType: "audio/mp4", Codecs: "mp4a.40.2", container: containerMP4},
	{SourceKind: "ogg_vorbis_comment", Codec: "vorbis", Delivery: DeliveryOriginal, MIMEType: "audio/ogg", Codecs: "vorbis", container: containerOgg},
	{SourceKind: "opus_tags", Codec: "opus", Delivery: DeliveryOriginal, MIMEType: "audio/ogg", Codecs: "opus", container: containerOgg},
}

// derivedFormatTable 是需要服务端派生才能播放的格式，只在派生可用（有可用的 ffmpeg 且自检通过）时进入格式表。
// ALAC 浏览器大多不能解码（只有 Safari 能），但它是无损的：转换成 FLAC 时位深、采样率与声道不变，
// 发布前核对两者解码后的 PCM 完全相同，所以交付的 FLAC 在音频上与源等价。
var derivedFormatTable = []Format{
	{SourceKind: "m4a", Codec: "alac", Delivery: DeliveryDerived, MIMEType: "audio/flac", container: containerMP4,
		input: derivation.Input{Demuxer: "mov", Decoder: "alac"}, policy: "alac-flac-lossless-v1", decoder: "alac"},
	{SourceKind: "ape_apev2", Codec: "ape", Delivery: DeliveryDerived, MIMEType: "audio/flac", container: containerAPE,
		input: derivation.Input{Demuxer: "ape", Decoder: "ape"}, policy: "ape-flac-lossless-v1", decoder: "ape"},
	{SourceKind: "wavpack_apev2", Codec: "wavpack", Delivery: DeliveryDerived, MIMEType: "audio/flac", container: containerWavPack,
		input: derivation.Input{Demuxer: "wv", Decoder: "wavpack"}, policy: "wavpack-flac-lossless-v1", decoder: "wavpack"},
	{SourceKind: "wavpack_apev2", Codec: "wavpack_hybrid", Delivery: DeliveryDerived, MIMEType: "audio/flac", container: containerWavPack,
		input: derivation.Input{Demuxer: "wv", Decoder: "wavpack"}, policy: "wavpack-flac-lossless-v1", decoder: "wavpack"},
	{SourceKind: "aiff_id3", Codec: "aiff", Delivery: DeliveryDerived, MIMEType: "audio/flac", container: containerAIFF,
		input: derivation.Input{Demuxer: "aiff"}, policy: "aiff-flac-lossless-v1", decoder: "pcm_s16be"},
}

// Formats 返回当前可交付的格式表（副本，顺序固定）：先是直出格式，派生可用时再加上派生格式。
func (service *Service) Formats() []Format {
	table := append([]Format(nil), formatTable...)
	if service.derivation != nil {
		for _, format := range derivedFormatTable {
			if service.derivation.Supports(format.decoder) {
				table = append(table, format)
			}
		}
	}
	return table
}

// findFormat 返回（来源类别、编码）对应的可交付格式；不在表中（或派生不可用）时 ok 为 false。
func (service *Service) findFormat(sourceKind, codec string) (format Format, ok bool) {
	for _, candidate := range service.Formats() {
		if candidate.SourceKind == sourceKind && candidate.Codec == codec {
			return candidate, true
		}
	}
	return Format{}, false
}
