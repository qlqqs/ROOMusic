// Package playback 拥有“把已入库曲目交给浏览器播放”的用例：哪些（来源类别、编码）组合获准交付、
// 播放时重新核对来源（目录仍启用、文件在目录内且仍是扫描时的那个）、安全地只读打开，以及已建立传输的周期复核。
// 旧接口仅直出浏览器可解码的原文件；新播放区经服务端渲染交付。
// 它不含 HTTP；传输层在 playback/httpapi。播放从不写音乐目录，也不转成有损格式。
package playback

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
	// DeliveryDerived 表示服务端处理后交付；保留旧值以兼容升级前已打开的标签页。
	DeliveryDerived Delivery = "derived"
)

// Format 是一个获准交付的（来源类别、编码）组合，以及交给浏览器的表示的媒体类型。
// MIMEType 与 Codecs 描述浏览器拿到的表示，而不是源文件：直出时是原文件的类型，服务端处理时是兼容表示的类型。
// Codecs 是 `codecs=` 参数的值，空表示没有该参数；前端用 MIMEType 与 Codecs 询问浏览器能否解码。
type Format struct {
	SourceKind string
	Codec      string
	Delivery   Delivery
	MIMEType   string
	Codecs     string
	// container 是源文件期望的容器，只用于核对文件头。
	container container
}

// formatTable 是直出格式表，只保留拿到验证证据的组合（见 M2a 设计的格式表）：表中格式都经真实 Chromium 自动化用合成样本验证。
// 不在表中的组合一律不直出：m4a/alac（通过渲染交付）、cue_virtual（仅经渲染精确切分交付）、
// ieee_float、wav_extensible 与其他 WAV 编码、未知组合。
var formatTable = []Format{
	{SourceKind: "flac_vorbis_comment", Codec: "flac", Delivery: DeliveryOriginal, MIMEType: "audio/flac", container: containerFLAC},
	{SourceKind: "mp3_id3v2", Codec: "mp3", Delivery: DeliveryOriginal, MIMEType: "audio/mpeg", container: containerMP3},
	{SourceKind: "wav_info", Codec: "pcm", Delivery: DeliveryOriginal, MIMEType: "audio/wav", Codecs: "1", container: containerWAV},
	{SourceKind: "m4a", Codec: "aac", Delivery: DeliveryOriginal, MIMEType: "audio/mp4", Codecs: "mp4a.40.2", container: containerMP4},
	{SourceKind: "ogg_vorbis_comment", Codec: "vorbis", Delivery: DeliveryOriginal, MIMEType: "audio/ogg", Codecs: "vorbis", container: containerOgg},
	{SourceKind: "opus_tags", Codec: "opus", Delivery: DeliveryOriginal, MIMEType: "audio/ogg", Codecs: "opus", container: containerOgg},
}

// Formats 返回当前可渲染的组合；旧直出接口独立使用 formatTable。
func (service *Service) Formats() []Format { return service.RenderFormats() }

// findFormat 只查旧接口的直出表，不受渲染能力影响。
func (service *Service) findFormat(sourceKind, codec string) (format Format, ok bool) {
	for _, candidate := range formatTable {
		if candidate.SourceKind == sourceKind && candidate.Codec == codec {
			return candidate, true
		}
	}
	return Format{}, false
}
