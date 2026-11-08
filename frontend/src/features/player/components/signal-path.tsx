import { AppWindow, FileAudio, Server, Speaker, X, type LucideIcon } from "lucide-react";
import { useId, useState, type CSSProperties } from "react";
import { Button, ModalDialog } from "../../../shared/ui";
import type { SourceFacts } from "../engine/queue";
import { usePlaybackCapabilities } from "../hooks/use-playback-capabilities";
import { usePlayerSelector } from "../player-provider";
import styles from "./signal-path.module.css";

type Quality = "lossless" | "lossy" | "unknown";

const losslessCodecs = new Set(["flac", "alac", "pcm"]);
const lossyCodecs = new Set(["mp3", "aac", "vorbis", "opus"]);

/** 整体质量只看源编码。 */
function qualityOf(source: SourceFacts | undefined): Quality {
  const codec = source?.codec?.toLowerCase();
  if (codec === undefined) return "unknown";
  if (losslessCodecs.has(codec)) return "lossless";
  return lossyCodecs.has(codec) ? "lossy" : "unknown";
}

const titles: Record<Quality, string> = { lossless: "信号路径：无损", lossy: "信号路径：有损", unknown: "信号路径" };

/** 数据源一行：无损写“编码 采样率 位深 声道”，有损写“编码 码率 采样率 声道”；缺的项省略。 */
function sourceLine(source: SourceFacts | undefined, quality: Quality): string {
  if (source === undefined) return "未知格式";
  const codec = source.codec === null ? null : source.codec.toUpperCase();
  const rate = source.sampleRate === null ? null : `${source.sampleRate / 1000}kHz`;
  const depth = source.bitDepth === null ? null : `${source.bitDepth}bit`;
  const bitrate = source.bitrate === null ? null : `${source.bitrate}kbps`;
  const channels = source.channels === null ? null : `${source.channels}ch`;
  const parts = quality === "lossy" ? [codec, bitrate, rate, channels] : [codec, rate, depth, channels];
  const line = parts.filter((part) => part !== null).join(" ");
  return line === "" ? "未知格式" : line;
}

/** 当前曲目的质量，播放栏与全屏底栏的圆点共用。 */
function useCurrentSource(): SourceFacts | undefined {
  return usePlayerSelector((snapshot) => snapshot.current?.track.source);
}

/** 信号路径圆点：无损金色实心、有损灰色实心、未知空心；名称里写明质量，不只靠颜色。点开是信号路径弹层。 */
export function SignalPathButton() {
  const source = useCurrentSource();
  const quality = qualityOf(source);
  const [anchor, setAnchor] = useState<DOMRect | null>(null);
  return <>
    <button type="button" className={styles.dot} data-quality={quality} aria-label={titles[quality]} title={titles[quality]} onClick={(event) => setAnchor(event.currentTarget.getBoundingClientRect())}>
      <span aria-hidden="true" />
    </button>
    <SignalPathDialog anchor={anchor} onClose={() => setAnchor(null)} />
  </>;
}

const layers: readonly { name: string; icon: LucideIcon }[] = [
  { name: "数据源", icon: FileAudio },
  { name: "服务端", icon: Server },
  { name: "浏览器", icon: AppWindow },
  { name: "系统输出", icon: Speaker },
];

/** 信号路径弹层：标题写出整体质量，各层一个图标、名称和一行取值。桌面在圆点上方，窄屏从底部弹出。 */
function SignalPathDialog({ anchor, onClose }: { anchor: DOMRect | null; onClose: () => void }) {
  const titleId = useId();
  const source = useCurrentSource();
  const capabilities = usePlaybackCapabilities();
  const quality = qualityOf(source);
  const table = capabilities.data;
  // 格式表还没取到时不替 ALAC 曲目说“原文件直出”。
  const delivery = source !== undefined && table === undefined && capabilities.isPending ? "pending" : source === undefined || table === undefined ? "original" : (table.deliveryOf({ sourceKind: source.sourceKind, codec: source.codec }) ?? "original");
  const server = delivery === "pending" ? "读取中" : delivery === "derived" ? `${source?.codec?.toUpperCase() ?? "ALAC"} → FLAC` : "原文件直出";
  const values = [sourceLine(source, quality), server, "解码", "系统默认输出"];
  const position = anchor === null ? undefined : ({ "--anchor-x": `${anchor.left + anchor.width / 2}px`, "--anchor-top": `${anchor.top}px` } as CSSProperties);
  return <ModalDialog open={anchor !== null} titleId={titleId} onRequestClose={onClose} closeOnBackdrop className={styles.dialog}>
    <div className={styles.content} style={position}>
      <div className={styles.header}>
        <h2 id={titleId} className={styles.title}>{titles[quality]}</h2>
        <Button variant="icon" icon={<X size={16} />} label="关闭" onClick={onClose} />
      </div>
      <ol role="list" className={styles.layers}>
        {layers.map(({ name, icon: Icon }, index) => <li key={name}>
          <Icon size={18} aria-hidden="true" className={styles.icon} />
          <div className={styles.layerText}><p className={styles.name}>{name}</p><p className={styles.value}>{values[index]}</p></div>
        </li>)}
      </ol>
    </div>
  </ModalDialog>;
}
