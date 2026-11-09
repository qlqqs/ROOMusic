import { AppWindow, AudioLines, FileAudio, Server, Speaker, X, type LucideIcon } from "lucide-react";
import { Fragment, useId, useState, type CSSProperties, type ReactNode } from "react";
import { Button, ModalDialog, useTooltip } from "../../../shared/ui";
import type { SourceFacts } from "../engine/queue";
import { useTrackAnalysis } from "../hooks/use-track-analysis";
import { usePlayerSelector } from "../player-provider";
import type { ZoneStream } from "../zones/model";
import styles from "./signal-path.module.css";

type Quality = "lossless" | "lossy" | "unknown";

const losslessCodecs = new Set(["flac", "alac", "pcm", "ape", "wavpack", "aiff"]);
// WavPack 混合模式没有 .wvc 时只有有损部分。
const lossyCodecs = new Set(["mp3", "aac", "vorbis", "opus", "wavpack_hybrid"]);

const codecNames = new Map([
  ["ape", "APE"],
  ["wavpack", "WavPack"],
  ["wavpack_hybrid", "WavPack"],
  ["aiff", "AIFF"],
]);

/** 数据源与服务端两行的编码名：表里有的用短名，其他写成大写。 */
function codecName(codec: string): string {
  return codecNames.get(codec.toLowerCase()) ?? codec.toUpperCase();
}

/** 整体质量：渲染流是省流量档时是有损，否则看源编码。 */
function qualityOf(source: SourceFacts | undefined, stream: ZoneStream | null): Quality {
  if (stream?.quality === "lossy") return "lossy";
  const codec = source?.codec?.toLowerCase();
  if (codec === undefined) return "unknown";
  if (losslessCodecs.has(codec)) return "lossless";
  return lossyCodecs.has(codec) ? "lossy" : "unknown";
}

const titles: Record<Quality, string> = { lossless: "信号路径：无损", lossy: "信号路径：有损", unknown: "信号路径" };

/** 数据源一行：无损写“编码 采样率 位深 声道”，有损写“编码 码率 采样率 声道”；缺的项省略。 */
function sourceLine(source: SourceFacts | undefined, quality: Quality): string {
  if (source === undefined) return "未知格式";
  const codec = source.codec === null ? null : codecName(source.codec);
  const rate = source.sampleRate === null ? null : `${source.sampleRate / 1000}kHz`;
  const depth = source.bitDepth === null ? null : `${source.bitDepth}bit`;
  const bitrate = source.bitrate === null ? null : `${source.bitrate}kbps`;
  const channels = source.channels === null ? null : `${source.channels}ch`;
  const parts = quality === "lossy" ? [codec, bitrate, rate, channels] : [codec, rate, depth, channels];
  const line = parts.filter((part) => part !== null).join(" ");
  return line === "" ? "未知格式" : line;
}

/** 服务端一行：渲染流的“编码 采样率 位深 声道”；没有在放的流时写“未在播放”，还不知道格式时写“未知格式”。 */
function streamLine(stream: ZoneStream | null): string {
  if (stream === null) return "未在播放";
  const { format } = stream;
  if (format === null) return "未知格式";
  const parts = [codecName(format.codec), format.sampleRate > 0 ? `${format.sampleRate / 1000}kHz` : null, format.bitDepth === null ? null : `${format.bitDepth}bit`, format.channels > 0 ? `${format.channels}ch` : null];
  return parts.filter((part) => part !== null).join(" ");
}

/** 响度数值：一位小数，负号用 −，0 写 0.0。 */
function formatLevel(value: number): string {
  const rounded = Math.round(value * 10) / 10;
  if (rounded === 0) return "0.0";
  return `${rounded < 0 ? "−" : ""}${Math.abs(rounded).toFixed(1)}`;
}

/** 响度一行：积分响度、响度范围、真峰值，缺的项省略；没有分析结果或三项都缺时是 null，整行不显示。每项连同后面的“·”不断行，只在项与项之间换行。 */
function useLoudnessLine(): ReactNode[] | null {
  const trackId = usePlayerSelector((snapshot) => snapshot.current?.track.trackId ?? null);
  const analysis = useTrackAnalysis(trackId);
  if (analysis === null) return null;
  const parts = [
    analysis.integratedLufs === null ? null : `${formatLevel(analysis.integratedLufs)} LUFS`,
    analysis.loudnessRangeLu === null ? null : `范围 ${formatLevel(analysis.loudnessRangeLu)} LU`,
    analysis.truePeakDbtp === null ? null : `峰值 ${formatLevel(analysis.truePeakDbtp)} dBTP`,
  ].filter((part) => part !== null);
  if (parts.length === 0) return null;
  return parts.map((part, index) => <Fragment key={part}>{index > 0 && " "}<span className={styles.nowrap}>{part}{index < parts.length - 1 && " ·"}</span></Fragment>);
}

/** 当前曲目的来源事实，播放栏与全屏底栏的圆点共用。 */
function useCurrentSource(): SourceFacts | undefined {
  return usePlayerSelector((snapshot) => snapshot.current?.track.source);
}

/** 正在控制的播放区的当前渲染流。 */
function useCurrentStream(): ZoneStream | null {
  return usePlayerSelector((snapshot) => snapshot.zone?.stream ?? null);
}

/** 信号路径圆点：无损金色实心、有损灰色实心、未知空心；名称里写明质量，不只靠颜色。点开是信号路径弹层。 */
export function SignalPathButton() {
  const source = useCurrentSource();
  const quality = qualityOf(source, useCurrentStream());
  const [anchor, setAnchor] = useState<DOMRect | null>(null);
  const tip = useTooltip(titles[quality]);
  return <>
    <button type="button" className={styles.dot} data-quality={quality} aria-label={titles[quality]} {...tip.triggerProps} onClick={(event) => setAnchor(event.currentTarget.getBoundingClientRect())}>
      <span aria-hidden="true" />
    </button>
    {tip.tooltip}
    <SignalPathDialog anchor={anchor} onClose={() => setAnchor(null)} />
  </>;
}

type Layer = { name: string; icon: LucideIcon; value: ReactNode };

/** 信号路径弹层：标题写出整体质量，各层一个图标、名称和一行取值；响度一层只在有分析结果时出现。桌面在圆点上方，窄屏从底部弹出。 */
function SignalPathDialog({ anchor, onClose }: { anchor: DOMRect | null; onClose: () => void }) {
  const titleId = useId();
  const source = useCurrentSource();
  const stream = useCurrentStream();
  const quality = qualityOf(source, stream);
  const loudness = useLoudnessLine();
  const layers: Layer[] = [
    { name: "数据源", icon: FileAudio, value: sourceLine(source, quality) },
    ...(loudness === null ? [] : [{ name: "响度", icon: AudioLines, value: loudness }]),
    { name: "服务端", icon: Server, value: streamLine(stream) },
    { name: "浏览器", icon: AppWindow, value: "解码" },
    { name: "系统输出", icon: Speaker, value: "系统默认输出" },
  ];
  const position = anchor === null ? undefined : ({ "--anchor-x": `${anchor.left + anchor.width / 2}px`, "--anchor-top": `${anchor.top}px` } as CSSProperties);
  return <ModalDialog open={anchor !== null} titleId={titleId} onRequestClose={onClose} closeOnBackdrop className={styles.dialog}>
    <div className={styles.content} style={position}>
      <div className={styles.header}>
        <h2 id={titleId} className={styles.title}>{titles[quality]}</h2>
        <Button variant="icon" icon={<X size={16} />} label="关闭" onClick={onClose} />
      </div>
      <ol role="list" className={styles.layers}>
        {layers.map(({ name, icon: Icon, value }) => <li key={name}>
          <Icon size={18} aria-hidden="true" className={styles.icon} />
          <div className={styles.layerText}><p className={styles.name}>{name}</p><p className={styles.value}>{value}</p></div>
        </li>)}
      </ol>
    </div>
  </ModalDialog>;
}
