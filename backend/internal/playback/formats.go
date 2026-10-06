// Package playback 拥有“把已入库曲目的原文件交给浏览器播放”的用例：哪些（来源类别、编码）组合获准直出、
// 播放时重新核对来源（目录仍启用、文件在目录内且仍是扫描时的那个）、安全地只读打开，以及已建立传输的周期复核。
// 它不含 HTTP；传输层在 playback/httpapi。M2a 只直出原文件，不转码、不重封装、不写任何文件。
package playback

// container 是格式表中的组合期望的文件容器，只用于核对文件头。
type container int

const (
	containerFLAC container = iota + 1
	containerMP4
	containerMP3
	containerWAV
	containerOgg
)

// Format 是一个获准直出的（来源类别、编码）组合，以及响应里声明的媒体类型。
// Codecs 是 `codecs=` 参数的值，空表示没有该参数；前端用 MIMEType 与 Codecs 询问浏览器能否解码。
type Format struct {
	SourceKind string
	Codec      string
	MIMEType   string
	Codecs     string
	container  container
}

// formatTable 只保留拿到验证证据的组合（见 M2a 设计的格式表）：表中格式都经真实 Chromium 自动化用合成样本验证。
// ALAC 只有 Safari 能解码，Safari 手工实播通过才加入（M2a 收尾时尚无证据，2026-10-06 移出）。不在表中的组合一律不直出：
// m4a/alac、cue_virtual（需要精确切分，M2b）、ieee_float、wav_extensible 与其他 WAV 编码、未知组合。
var formatTable = []Format{
	{SourceKind: "flac_vorbis_comment", Codec: "flac", MIMEType: "audio/flac", container: containerFLAC},
	{SourceKind: "mp3_id3v2", Codec: "mp3", MIMEType: "audio/mpeg", container: containerMP3},
	{SourceKind: "wav_info", Codec: "pcm", MIMEType: "audio/wav", Codecs: "1", container: containerWAV},
	{SourceKind: "m4a", Codec: "aac", MIMEType: "audio/mp4", Codecs: "mp4a.40.2", container: containerMP4},
	{SourceKind: "ogg_vorbis_comment", Codec: "vorbis", MIMEType: "audio/ogg", Codecs: "vorbis", container: containerOgg},
	{SourceKind: "opus_tags", Codec: "opus", MIMEType: "audio/ogg", Codecs: "opus", container: containerOgg},
}

// Formats 返回获准直出的格式表（副本，顺序固定）。
func Formats() []Format {
	return append([]Format(nil), formatTable...)
}

// FindFormat 返回（来源类别、编码）对应的直出格式；不在表中时 ok 为 false。
func FindFormat(sourceKind, codec string) (format Format, ok bool) {
	for _, candidate := range formatTable {
		if candidate.SourceKind == sourceKind && candidate.Codec == codec {
			return candidate, true
		}
	}
	return Format{}, false
}
