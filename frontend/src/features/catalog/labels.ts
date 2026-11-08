// 署名角色与证据词表的中文名。清单按 2026-10-02 后端实际写入的取值逐一核对：
// 署名角色见 internal/library/parser/parser.go 的 setAudioTag，字段、来源、可信度、处理方式与原因码见 internal/catalog/organizer.go
// 和 internal/library/parser，归组类型是 organizer.go 的 CandidateKind。
// 接口把它们都按字符串返回：后端新增取值不会让页面出错，未知值原样显示（这些取值只含小写字母、数字与下划线，可以安全显示）。
function labeller(names: Readonly<Record<string, string>>): (value: string) => string {
  const known = new Map(Object.entries(names));
  return (value) => known.get(value) ?? value;
}

/** 署名角色（专辑级与曲目级共用）。 */
export const creditRoleLabel = labeller({
  album_artist: "专辑艺人",
  lyricist: "作词",
  composer: "作曲",
  arranger: "编曲",
  conductor: "指挥",
  performer: "演奏",
  producer: "制作人",
});

/** 署名的细分：乐器与工种的规范键（后端 metadata/credit_roles.go）；其余是标签或歌词里的原写法，原样显示。 */
export const creditDetailLabel = labeller({
  violin: "小提琴",
  viola: "中提琴",
  cello: "大提琴",
  double_bass: "低音提琴",
  bass: "贝斯",
  guitar: "吉他",
  piano: "钢琴",
  keyboards: "键盘",
  organ: "管风琴",
  harpsichord: "羽管键琴",
  synthesizer: "合成器",
  drums: "鼓",
  percussion: "打击乐",
  flute: "长笛",
  clarinet: "单簧管",
  oboe: "双簧管",
  bassoon: "巴松管",
  saxophone: "萨克斯",
  trumpet: "小号",
  trombone: "长号",
  horn: "圆号",
  harp: "竖琴",
  erhu: "二胡",
  pipa: "琵琶",
  guzheng: "古筝",
  dizi: "笛子",
  vocals: "演唱",
  backing_vocals: "和声",
  choir: "合唱",
  orchestra: "乐团",
  mix: "混音",
  mastering: "母带",
  recording: "录音",
  remix: "重混",
  dj_mix: "DJ 混音",
  programming: "编程",
  libretto: "剧本",
});

/** 证据所说明的字段：与发行版本详情里的同名字段一一对应。 */
export const evidenceFieldLabel = labeller({
  title: "标题",
  artist: "艺人",
  album_artist: "专辑艺人",
  year: "年份",
  genre: "流派",
  label: "厂牌",
  catalog_number: "编号",
  edition: "版本说明",
  barcode: "条码",
  source_type: "来源类型",
  media_type: "介质类型",
});

/** 取值的出处。带 fallback 的是整理时没有找到可靠来源、退而采用的推断。 */
export const evidenceSourceLabel = labeller({
  tag: "音频标签",
  folder: "目录名",
  folder_structure: "目录结构",
  cue_sheet: "CUE 文件（专辑）",
  cue_track: "CUE 文件（分轨）",
  rip_log: "抓轨日志",
  compilation: "合辑",
  folder_fallback: "目录名（推断）",
  filename_fallback: "文件名（推断）",
  path_order_fallback: "路径顺序（推断）",
  display_fallback: "默认显示值（推断）",
  default_fallback: "默认值（推断）",
});

export const confidenceLabel = labeller({
  high: "高",
  medium: "中",
  low: "低",
});

/** 整理时对这个字段的处理：不确定时仍然采用了一个值，所以“需要关注”只是提示核对。 */
export const evidenceActionLabel = labeller({
  auto_apply: "直接采用",
  uncertain_apply: "不确定，已暂时采用",
});

/** 字段证据与归组证据里的原因码。 */
export const reasonCodeLabel = labeller({
  field_missing: "没有找到这个字段",
  inconsistent_candidates: "候选值互相不一致",
  inferred_value: "取值由推断得到",
  same_directory_conflict: "同一目录里的内容无法确定归属",
});

/** 归组方式：整理时怎样把一批音频文件判定为同一个发行版本。 */
export const candidateKindLabel = labeller({
  ordinary_directory: "普通目录",
  strict_multidisc: "多碟目录结构",
  box_leaf: "套装里的子目录",
  same_dir_split: "同一目录里拆分",
  format_split: "同一目录按格式分开",
  loose_album: "散放的专辑",
  loose_unknown: "散放的未知文件",
});

/**
 * 音频编码的显示名。清单按扫描器写入的取值核对（internal/library/parser/parser.go 的 Codec；WAV 按格式标签区分 pcm、ieee_float 与 wav_extensible；
 * WavPack 按块头标志区分整数、混合、浮点与 DSD；AIFF 按 AIFC 压缩类型区分整数与浮点）。
 * 接口按字符串返回，未知取值原样显示（如其他压缩类型的 aifc_*）。
 */
export const codecLabel = labeller({
  flac: "FLAC",
  alac: "ALAC",
  aac: "AAC",
  mp3: "MP3",
  vorbis: "Vorbis",
  opus: "Opus",
  wav: "WAV",
  pcm: "WAV（PCM）",
  ieee_float: "WAV（浮点）",
  wav_extensible: "WAV（扩展格式）",
  ape: "APE",
  wavpack: "WavPack",
  wavpack_hybrid: "WavPack（有损）",
  wavpack_float: "WavPack（浮点）",
  wavpack_dsd: "WavPack（DSD）",
  aiff: "AIFF",
  aiff_float: "AIFF（浮点）",
});
