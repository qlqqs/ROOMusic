import { MonitorSpeaker } from "lucide-react";
import { useId, useRef, useState } from "react";
import {
  crossfadeDurations,
  customSampleRates,
  levelingModes,
  sampleRateFilters,
  sampleRateStrategies,
  streamQualities,
  type CrossfadeSeconds,
  type LevelingMode,
  type SampleRateFilter,
  type SampleRateStrategy,
  type StreamQuality,
} from "../zones/model";
import { usePlayerEngine, usePlayerSelector } from "../player-provider";
import { gainLabel } from "./player-labels";
import styles from "./playback-settings-form.module.css";

const levelingLabels: Record<LevelingMode, string> = { off: "关", auto: "自动", track: "按曲目", album: "按专辑" };
const qualityLabels: Record<StreamQuality, string> = { lossless: "无损", lossy: "省流量" };
const strategyLabels: Record<SampleRateStrategy, string> = { none: "不转换", max_sample_rate: "设备最高采样率", power_of_2: "最高整数倍采样率", custom: "指定采样率" };
const filterLabels: Record<SampleRateFilter, string> = {
  linear_precise: "精确 · 线性相位",
  minimum_precise: "精确 · 最小相位",
  linear_smooth: "平滑 · 线性相位",
  minimum_smooth: "平滑 · 最小相位",
};
/** 选“指定采样率”时还没选过具体值，先用 96 kHz。 */
const DEFAULT_CUSTOM_RATE = 96000;

type Option<T extends string | number> = { value: T; label: string };

/** 分段选择：一组单选按钮，看起来是相连的几段。 */
function Segmented<T extends string | number>({ legend, options, value, disabled, onChange }: { legend: string; options: readonly Option<T>[]; value: T; disabled: boolean; onChange: (value: T) => void }) {
  const name = useId();
  return (
    <fieldset className={styles.field} disabled={disabled}>
      <legend className={styles.legend}>{legend}</legend>
      <div className={styles.segments}>
        {options.map((option) => (
          <label key={option.value} className={styles.segment}>
            <input type="radio" name={name} checked={option.value === value} onChange={() => onChange(option.value)} />
            <span>{option.label}</span>
          </label>
        ))}
      </div>
    </fieldset>
  );
}

/** 开关：带 role="switch" 的复选框。 */
function Switch({ label, checked, disabled, onChange }: { label: string; checked: boolean; disabled: boolean; onChange: (checked: boolean) => void }) {
  return (
    <label className={styles.switchRow}>
      <span className={styles.legend}>{label}</span>
      <input type="checkbox" role="switch" className={styles.switch} checked={checked} disabled={disabled} onChange={(event) => onChange(event.target.checked)} />
    </label>
  );
}

/** 下拉选择：选中即提交。 */
function Select<T extends string | number>({ label, options, value, disabled, onChange }: { label: string; options: readonly Option<T>[]; value: T; disabled: boolean; onChange: (value: T) => void }) {
  const id = useId();
  return (
    <div className={styles.field} data-disabled={disabled ? "" : undefined}>
      <label htmlFor={id} className={styles.legend}>{label}</label>
      <select id={id} className={styles.select} value={value} disabled={disabled} onChange={(event) => {
        const picked = options.find((option) => String(option.value) === event.target.value);
        if (picked !== undefined) onChange(picked.value);
      }}>
        {options.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
      </select>
    </div>
  );
}

/** Headroom 增益滑块：拖动时只改本地显示，松手、按键抬起或离开时才发出，同一个草稿值只发一次；服务端的值变了就跟上服务端。 */
function GainSlider({ value, disabled, onCommit }: { value: number; disabled: boolean; onCommit: (db: number) => void }) {
  const id = useId();
  // 每次拖动换一个草稿对象；发出过的草稿记在 ref 里（松手与失焦可能在同一批事件里，等不到重新渲染）。
  const [draft, setDraft] = useState<{ db: number } | null>(null);
  const sent = useRef<{ db: number } | null>(null);
  const [seen, setSeen] = useState(value);
  if (value !== seen) {
    setSeen(value);
    setDraft(null);
  }
  const shown = draft?.db ?? value;
  const commit = () => {
    if (draft === null || draft === sent.current || draft.db === value) return;
    sent.current = draft;
    onCommit(draft.db);
  };
  return (
    <div className={styles.field} data-disabled={disabled ? "" : undefined}>
      <label htmlFor={id} className={styles.legend}>增益</label>
      <div className={styles.slider}>
        <input id={id} type="range" min={-30} max={0} step={0.1} value={shown} aria-valuetext={gainLabel(shown)} disabled={disabled} onChange={(event) => setDraft({ db: Math.round(Number(event.target.value) * 10) / 10 })} onPointerUp={commit} onKeyUp={commit} onBlur={commit} />
        <output htmlFor={id} className={styles.sliderValue}>{gainLabel(shown)}</output>
      </div>
    </div>
  );
}

/** 设置页标题旁的设备名：这些设置改的是正在控制的那台设备。 */
export function PlaybackSettingsDevice() {
  const name = usePlayerSelector((snapshot) => snapshot.device?.name ?? null);
  if (name === null) return null;
  return <span className={styles.device}><MonitorSpeaker size={14} aria-hidden="true" /><span className={styles.deviceName}>{name}</span></span>;
}

/** 设置 › 播放：音量均衡、无缝、交叉渐变、音质、采样率转换与 Headroom，作用于正在控制的播放区（服务端渲染）；还没读到播放区时禁用。 */
export function PlaybackSettingsForm() {
  const engine = usePlayerEngine();
  const settings = usePlayerSelector((snapshot) => snapshot.settings);
  const disabled = usePlayerSelector((snapshot) => snapshot.zone === null);
  return (
    <div className={styles.form}>
      <Segmented
        legend="音量均衡"
        options={levelingModes.map((mode) => ({ value: mode, label: levelingLabels[mode] }))}
        value={settings.leveling}
        disabled={disabled}
        onChange={(leveling) => engine.setPlaybackSettings({ leveling })}
      />
      <Switch label="无缝播放" checked={settings.gapless} disabled={disabled} onChange={(gapless) => engine.setPlaybackSettings({ gapless })} />
      <Switch label="交叉渐变" checked={settings.crossfade} disabled={disabled} onChange={(crossfade) => engine.setPlaybackSettings({ crossfade })} />
      <Segmented<CrossfadeSeconds>
        legend="渐变时长"
        options={crossfadeDurations.map((seconds) => ({ value: seconds, label: `${seconds} 秒` }))}
        value={settings.crossfadeSeconds}
        disabled={disabled || !settings.crossfade}
        onChange={(crossfadeSeconds) => engine.setPlaybackSettings({ crossfadeSeconds })}
      />
      <Segmented
        legend="音质"
        options={streamQualities.map((quality) => ({ value: quality, label: qualityLabels[quality] }))}
        value={settings.quality}
        disabled={disabled}
        onChange={(quality) => engine.setPlaybackSettings({ quality })}
      />
      <Select<SampleRateStrategy>
        label="采样率转换"
        options={sampleRateStrategies.map((strategy) => ({ value: strategy, label: strategyLabels[strategy] }))}
        value={settings.sampleRate.strategy}
        disabled={disabled}
        onChange={(strategy) => engine.setPlaybackSettings({ sampleRate: { ...settings.sampleRate, strategy, customRate: strategy === "custom" ? (settings.sampleRate.customRate ?? DEFAULT_CUSTOM_RATE) : settings.sampleRate.customRate } })}
      />
      {settings.sampleRate.strategy === "custom" && (
        <Select<number>
          label="目标采样率"
          options={customSampleRates.map((rate) => ({ value: rate, label: `${rate / 1000} kHz` }))}
          value={settings.sampleRate.customRate ?? DEFAULT_CUSTOM_RATE}
          disabled={disabled}
          onChange={(customRate) => engine.setPlaybackSettings({ sampleRate: { ...settings.sampleRate, customRate } })}
        />
      )}
      <Select<SampleRateFilter>
        label="转换滤波器"
        options={sampleRateFilters.map((filter) => ({ value: filter, label: filterLabels[filter] }))}
        value={settings.sampleRate.filter}
        disabled={disabled}
        onChange={(filter) => engine.setPlaybackSettings({ sampleRate: { ...settings.sampleRate, filter } })}
      />
      <Switch label="Headroom" checked={settings.headroom.enabled} disabled={disabled} onChange={(enabled) => engine.setPlaybackSettings({ headroom: { ...settings.headroom, enabled } })} />
      <GainSlider value={settings.headroom.db} disabled={disabled || !settings.headroom.enabled} onCommit={(db) => engine.setPlaybackSettings({ headroom: { ...settings.headroom, db } })} />
      <Switch label="削波指示" checked={settings.headroom.clipIndicator} disabled={disabled} onChange={(clipIndicator) => engine.setPlaybackSettings({ headroom: { ...settings.headroom, clipIndicator } })} />
    </div>
  );
}
