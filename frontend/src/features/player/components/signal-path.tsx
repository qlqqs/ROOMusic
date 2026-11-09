import { ArrowDownToLine, ArrowLeftRight, AudioLines, AudioWaveform, Binary, FileAudio, Gauge, Grip, Merge, MonitorSpeaker, SlidersHorizontal, X, type LucideIcon } from "lucide-react";
import { Fragment, useId, useState, type CSSProperties, type ReactNode } from "react";
import { Button, ModalDialog, StatusTag, useTooltip } from "../../../shared/ui";
import { useTrackAnalysis } from "../hooks/use-track-analysis";
import { usePlayerSelector } from "../player-provider";
import type { Signal, SignalQuality, SignalStep } from "../zones/model";
import { gainLabel } from "./player-labels";
import styles from "./signal-path.module.css";

const qualityLabels: Record<SignalQuality, string> = { lossless: "无损", enhanced: "增强", high: "高质量", low: "有损" };

const codecNames = new Map([
  ["ape", "APE"],
  ["wavpack", "WavPack"],
  ["wavpack_hybrid", "WavPack"],
  ["aiff", "AIFF"],
  ["opus", "Opus"],
  ["vorbis", "Vorbis"],
]);

/** 编码名：表里有的用短名，其他写成大写。 */
function codecName(codec: string): string {
  return codecNames.get(codec.toLowerCase()) ?? codec.toUpperCase();
}

function rateLabel(sampleRate: number): string {
  return `${sampleRate / 1000} kHz`;
}

function formatLabel(codec: string, sampleRate: number, bitDepth: number | null): string {
  return `${codecName(codec)} ${rateLabel(sampleRate)}${bitDepth === null ? "" : ` ${bitDepth} bit`}`;
}

const stepIcons: Record<SignalStep["kind"], LucideIcon> = {
  source: FileAudio,
  convert: ArrowLeftRight,
  downmix: Merge,
  leveling: SlidersHorizontal,
  headroom: ArrowDownToLine,
  dither: Grip,
  encode: Binary,
};

function stepText(step: SignalStep): string {
  switch (step.kind) {
    case "source":
      return formatLabel(step.params.codec, step.params.sampleRate, step.params.bitDepth);
    case "convert":
      return `采样率 ${rateLabel(step.params.fromRate)} → ${rateLabel(step.params.toRate)}`;
    case "downmix":
      return `声道 ${step.params.fromChannels} → ${step.params.toChannels}`;
    case "leveling":
      return `电平化 ${gainLabel(step.params.gainDb)}`;
    case "headroom":
      return `Headroom ${gainLabel(step.params.gainDb)}`;
    case "dither":
      return `${step.params.kind.toUpperCase()} 抖动 ${step.params.bits} bit`;
    case "encode":
      return step.params.bitrateKbps === null ? formatLabel(step.params.codec, step.params.sampleRate, step.params.bitDepth) : `${codecName(step.params.codec)} ${step.params.bitrateKbps} kbps`;
  }
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

/** 名称与悬停提示写出整体质量与是否在削波，不只靠颜色。 */
function titleOf(signal: Signal): string {
  return `信号路径：${qualityLabels[signal.quality]}${signal.clipping === true ? "，削波" : ""}`;
}

/** 正在控制的播放区里正在放的那首的信号路径。 */
function useSignal(): Signal | null {
  return usePlayerSelector((snapshot) => snapshot.zone?.signal ?? null);
}

/** 信号路径按钮：只有图标，颜色按整体质量，削波时是警示色；没有信号路径时不显示。点开是信号路径弹层。 */
export function SignalPathButton() {
  const signal = useSignal();
  const [anchor, setAnchor] = useState<DOMRect | null>(null);
  const tip = useTooltip(signal === null ? undefined : titleOf(signal));
  if (signal === null) {
    if (anchor !== null) setAnchor(null);
    return null;
  }
  const title = titleOf(signal);
  return <>
    <button type="button" className={styles.trigger} data-quality={signal.quality} data-clipping={signal.clipping === true ? "" : undefined} aria-label={title} {...tip.triggerProps} onClick={(event) => setAnchor(event.currentTarget.getBoundingClientRect())}>
      <AudioWaveform size={18} aria-hidden="true" />
    </button>
    {tip.tooltip}
    <SignalPathDialog signal={signal} anchor={anchor} onClose={() => setAnchor(null)} />
  </>;
}

type Row = { key: string; icon: LucideIcon; text: ReactNode; quality: SignalQuality | null; tags: readonly string[] };

/** 信号路径弹层：各步一行（图标、一行短文字，有损与兼容转换另有标记），来源之后是响度（有分析结果时），最后是处理速度与输出设备。桌面在按钮上方，窄屏从底部弹出。 */
function SignalPathDialog({ signal, anchor, onClose }: { signal: Signal; anchor: DOMRect | null; onClose: () => void }) {
  const titleId = useId();
  const deviceName = usePlayerSelector((snapshot) => snapshot.device?.name ?? null);
  const loudness = useLoudnessLine();
  const rows: Row[] = signal.steps.flatMap((step, index) => {
    const row: Row = {
      key: `${index}-${step.kind}`,
      icon: stepIcons[step.kind],
      text: stepText(step),
      quality: step.quality,
      tags: [...(step.lossy ? ["有损"] : []), ...(step.compat ? ["兼容转换"] : [])],
    };
    return step.kind === "source" && loudness !== null ? [row, { key: "loudness", icon: AudioLines, text: loudness, quality: null, tags: [] }] : [row];
  });
  if (signal.processingSpeed !== null) rows.push({ key: "speed", icon: Gauge, text: `处理速度 ${signal.processingSpeed.toFixed(1)}×`, quality: null, tags: [] });
  if (deviceName !== null) rows.push({ key: "device", icon: MonitorSpeaker, text: deviceName, quality: null, tags: [] });
  const position = anchor === null ? undefined : ({ "--anchor-x": `${anchor.left + anchor.width / 2}px`, "--anchor-top": `${anchor.top}px` } as CSSProperties);
  return <ModalDialog open={anchor !== null} titleId={titleId} onRequestClose={onClose} closeOnBackdrop className={styles.dialog}>
    <div className={styles.content} style={position}>
      <div className={styles.header}>
        <h2 id={titleId} className={styles.title}>{titleOf(signal)}</h2>
        <Button variant="icon" icon={<X size={16} />} label="关闭" onClick={onClose} />
      </div>
      <ol role="list" className={styles.steps}>
        {rows.map(({ key, icon: Icon, text, quality, tags }) => <li key={key}>
          <Icon size={16} aria-hidden="true" className={styles.icon} data-quality={quality ?? undefined} />
          <span className={styles.text}>{text}</span>
          {tags.map((tag) => <StatusTag key={tag}>{tag}</StatusTag>)}
        </li>)}
      </ol>
    </div>
  </ModalDialog>;
}
