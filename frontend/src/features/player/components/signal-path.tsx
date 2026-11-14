import { ArrowDownToLine, ArrowLeftRight, AudioWaveform, Binary, FileAudio, Grip, Merge, MonitorSpeaker, SlidersHorizontal, SlidersVertical, Waves, X, Zap, type LucideIcon } from "lucide-react";
import { useId, useState, type CSSProperties } from "react";
import { dsdRateName } from "../../../shared/lib/dsd";
import { Button, ModalDialog, StatusTag, useTooltip, VisuallyHidden } from "../../../shared/ui";
import { useTrackAnalysis } from "../hooks/use-track-analysis";
import { usePlayerSelector } from "../player-provider";
import type { Signal, SignalQuality, SignalStep } from "../zones/model";
import { gainLabel, sourceFormat } from "./player-labels";
import styles from "./signal-path.module.css";

const qualityLabels: Record<SignalQuality, string> = { lossless: "无损", enhanced: "增强", high: "高质量", low: "有损" };
const qualityIcons: Record<SignalQuality, LucideIcon> = { lossless: AudioWaveform, enhanced: SlidersHorizontal, high: ArrowLeftRight, low: AudioWaveform };

const codecNames = new Map([
  ["ape", "APE"],
  ["wavpack", "WavPack"],
  ["wavpack_hybrid", "WavPack"],
  ["aiff", "AIFF"],
  ["dsf", "DSD（DSF）"],
  ["dff", "DSD（DFF）"],
  ["opus", "Opus"],
  ["vorbis", "Vorbis"],
]);

/** 编码名：表里有的用短名，其他写成大写。 */
function codecName(codec: string): string {
  return codecNames.get(codec.toLowerCase()) ?? codec.toUpperCase();
}

function rateLabel(sampleRate: number): string {
  return dsdRateName(sampleRate) ?? `${sampleRate / 1000} kHz`;
}

function formatLabel(codec: string, sampleRate: number, bitDepth: number | null): string {
  const dsd = dsdRateName(sampleRate);
  if (dsd !== null) return `${codecName(codec)} ${dsd}`;
  return `${codecName(codec)} ${rateLabel(sampleRate)}${bitDepth === null ? "" : ` ${bitDepth} bit`}`;
}

const stepIcons: Record<SignalStep["kind"], LucideIcon> = {
  source: FileAudio,
  convert: ArrowLeftRight,
  sample_rate: ArrowLeftRight,
  dsd_to_pcm: Zap,
  downmix: Merge,
  peq: SlidersVertical,
  convolution: Waves,
  leveling: SlidersHorizontal,
  headroom: ArrowDownToLine,
  dither: Grip,
  encode: Binary,
};

/** 链上一步的两行：上面是步骤名，下面是取值。 */
function stepParts(step: SignalStep): { label: string; value: string } {
  switch (step.kind) {
    case "source":
      return { label: "来源", value: formatLabel(step.params.codec, step.params.sampleRate, step.params.bitDepth) };
    case "convert":
    case "sample_rate":
      return { label: "采样率转换", value: `${rateLabel(step.params.fromRate)} → ${rateLabel(step.params.toRate)}` };
    case "dsd_to_pcm":
      return { label: "DSD 转 PCM", value: `${step.params.from} → PCM ${rateLabel(step.params.toRate)}` };
    case "downmix":
      return { label: step.params.mode === "select" ? "声道选择" : "降混", value: `${step.params.fromLayout ?? step.params.fromChannels} → ${step.params.toLayout ?? step.params.toChannels}` };
    case "peq":
      return { label: "参数均衡", value: `${step.params.label} · ${step.params.bands} 段` };
    case "convolution":
      return { label: "卷积", value: `${step.params.name} · ${step.params.taps} 抽头` };
    case "leveling":
      return { label: "电平化", value: gainLabel(step.params.gainDb) };
    case "headroom":
      return { label: "Headroom", value: gainLabel(step.params.gainDb) };
    case "dither":
      return { label: "抖动", value: `${step.params.kind.toUpperCase()} ${step.params.bits} bit` };
    case "encode":
      return { label: "编码", value: step.params.bitrateKbps === null ? formatLabel(step.params.codec, step.params.sampleRate, step.params.bitDepth) : `${codecName(step.params.codec)} ${step.params.bitrateKbps} kbps` };
  }
}

/** 响度数值：一位小数，负号用 −，0 写 0.0。 */
function formatLevel(value: number): string {
  const rounded = Math.round(value * 10) / 10;
  if (rounded === 0) return "0.0";
  return `${rounded < 0 ? "−" : ""}${Math.abs(rounded).toFixed(1)}`;
}

type Measurement = { label: string; value: string };

/** 底部测量格：响度、范围、峰值（当前曲目的分析结果）与处理速度，缺的项不显示。 */
function useMeasurements(signal: Signal): Measurement[] {
  const trackId = usePlayerSelector((snapshot) => snapshot.current?.track.trackId ?? null);
  const analysis = useTrackAnalysis(trackId);
  const lufs = analysis?.integratedLufs ?? null;
  const range = analysis?.loudnessRangeLu ?? null;
  const peak = analysis?.truePeakDbtp ?? null;
  const cells: (Measurement | null)[] = [
    lufs === null ? null : { label: "响度", value: `${formatLevel(lufs)} LUFS` },
    range === null ? null : { label: "范围", value: `${formatLevel(range)} LU` },
    peak === null ? null : { label: "峰值", value: `${formatLevel(peak)} dBTP` },
    signal.processingSpeed === null ? null : { label: "处理速度", value: `${signal.processingSpeed.toFixed(1)}×` },
  ];
  return cells.filter((cell) => cell !== null);
}

/** 名称与悬停提示写出整体质量与是否在削波，不只靠颜色。 */
function titleOf(signal: Signal): string {
  return `信号路径：${qualityLabels[signal.quality]}${signal.clipping === true ? "，削波" : ""}`;
}

/** 正在控制的播放区里正在放的那首的信号路径。 */
function useSignal(): Signal | null {
  return usePlayerSelector((snapshot) => snapshot.zone?.signal ?? null);
}

/**
 * 信号路径入口：一个胶囊按钮，质量色圆点加来源格式（如“FLAC 24/96”）。可访问名是“格式，信号路径：质量”，含可见文字。
 * 播放中圆点约 4 秒一次呼吸，暂停、缓冲、出错时静止；削波时变警示色并快速脉动（动画只在 CSS 里，减少动态效果时没有）。
 * 没有信号路径时只是格式文字，不是按钮；没有当前曲目时不渲染。
 */
export function SignalPathButton() {
  const signal = useSignal();
  const track = usePlayerSelector((snapshot) => snapshot.current?.track ?? null);
  const breathing = usePlayerSelector((snapshot) => snapshot.status === "playing" && snapshot.fault === null);
  const [anchor, setAnchor] = useState<DOMRect | null>(null);
  const tip = useTooltip(signal === null ? undefined : titleOf(signal));
  if (signal === null || track === null) {
    if (anchor !== null) setAnchor(null);
    return track === null ? null : <span className={styles.plain}>{sourceFormat(track)}</span>;
  }
  const format = sourceFormat(track);
  const clipping = signal.clipping === true;
  return <>
    <button type="button" className={styles.trigger} data-quality={signal.quality} data-clipping={clipping ? "" : undefined} data-state={breathing ? "breathing" : undefined} aria-label={`${format}，${titleOf(signal)}`} {...tip.triggerProps} onClick={(event) => setAnchor(event.currentTarget.getBoundingClientRect())}>
      <span className={styles.dot} aria-hidden="true" />
      <span className={styles.format}>{format}</span>
    </button>
    {tip.tooltip}
    <SignalPathDialog signal={signal} anchor={anchor} onClose={() => setAnchor(null)} />
  </>;
}

/** 决定整体质量的那一步：链上第一个与整体评级相同的步骤；整体是无损时没有哪一步拉低质量，不标。 */
function worstIndex(signal: Signal): number {
  if (signal.quality === "lossless") return -1;
  return signal.steps.findIndex((step) => step.quality === signal.quality);
}

/**
 * 信号路径弹层（340px）：头部是质量词与“来源格式 → 输出设备”，削波时下面一条“正在削波”；中间是从来源到输出设备的竖向链，
 * 每步“步骤名 + 取值”，最差一步加粗；底部是测量格。桌面在按钮上方，窄屏从底部弹出。
 */
function SignalPathDialog({ signal, anchor, onClose }: { signal: Signal; anchor: DOMRect | null; onClose: () => void }) {
  const titleId = useId();
  const deviceName = usePlayerSelector((snapshot) => snapshot.device?.name ?? null);
  const measurements = useMeasurements(signal);
  const source = signal.steps.find((step) => step.kind === "source");
  const sourceText = source === undefined ? null : stepParts(source).value;
  const subtitle = [sourceText, deviceName].filter((part) => part !== null).join(" → ");
  const worst = worstIndex(signal);
  const QualityIcon = qualityIcons[signal.quality];
  const position = anchor === null ? undefined : ({ "--anchor-x": `${anchor.left + anchor.width / 2}px`, "--anchor-top": `${anchor.top}px` } as CSSProperties);
  return <ModalDialog open={anchor !== null} titleId={titleId} onRequestClose={onClose} closeOnBackdrop className={styles.dialog}>
    <div className={styles.content} style={position} data-quality={signal.quality}>
      <div className={styles.header}>
        <span className={styles.badge} aria-hidden="true"><QualityIcon size={20} /></span>
        <div className={styles.heading}>
          <h2 id={titleId} className={styles.title}><VisuallyHidden>信号路径：</VisuallyHidden>{qualityLabels[signal.quality]}{signal.clipping === true ? <VisuallyHidden>，削波</VisuallyHidden> : null}</h2>
          {subtitle === "" ? null : <p className={styles.subtitle}>{subtitle}</p>}
        </div>
        <Button variant="icon" icon={<X size={16} />} label="关闭" onClick={onClose} />
      </div>
      {signal.clipping === true ? <p className={styles.clipping}>正在削波</p> : null}
      <ol role="list" className={styles.chain}>
        {signal.steps.map((step, index) => {
          const Icon = stepIcons[step.kind];
          const { label, value } = stepParts(step);
          return <li key={`${index}-${step.kind}`} data-worst={index === worst ? "" : undefined}>
            <span className={styles.node} data-quality={step.quality} aria-hidden="true"><Icon size={12} /></span>
            <span className={styles.step}>
              <span className={styles.label}>{label}</span>
              <span className={styles.value}>{value}{step.lossy ? <StatusTag>有损</StatusTag> : null}{step.compat ? <StatusTag>兼容转换</StatusTag> : null}</span>
            </span>
          </li>;
        })}
        {deviceName === null ? null : <li>
          <span className={`${styles.node} ${styles.output}`} data-quality={signal.quality} aria-hidden="true"><MonitorSpeaker size={12} /></span>
          <span className={styles.step}><span className={styles.label}>输出</span><span className={styles.value}>{deviceName}</span></span>
        </li>}
      </ol>
      {measurements.length === 0 ? null : <dl className={styles.measurements}>
        {measurements.map(({ label, value }) => <div key={label}><dt>{label}</dt><dd>{value}</dd></div>)}
      </dl>}
    </div>
  </ModalDialog>;
}
