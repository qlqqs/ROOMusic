import { MonitorSpeaker } from "lucide-react";
import { useId, useState } from "react";
import { crossfadeDurations, levelingModes, streamQualities, type CrossfadeSeconds, type LevelingMode, type StreamQuality } from "../zones/model";
import { usePlayerEngine, usePlayerSelector } from "../player-provider";
import { gainLabel } from "./player-labels";
import styles from "./playback-settings-form.module.css";

const levelingLabels: Record<LevelingMode, string> = { off: "关", auto: "自动", track: "按曲目", album: "按专辑" };
const qualityLabels: Record<StreamQuality, string> = { lossless: "无损", lossy: "省流量" };

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

/** Headroom 增益滑块：拖动时只改本地显示，松手、按键抬起或离开时才发出；服务端的值变了就跟上服务端。 */
function GainSlider({ value, disabled, onCommit }: { value: number; disabled: boolean; onCommit: (db: number) => void }) {
  const id = useId();
  const [draft, setDraft] = useState<number | null>(null);
  const [seen, setSeen] = useState(value);
  if (value !== seen) {
    setSeen(value);
    setDraft(null);
  }
  const shown = draft ?? value;
  const commit = () => {
    if (draft !== null && draft !== value) onCommit(draft);
  };
  return (
    <div className={styles.field} data-disabled={disabled ? "" : undefined}>
      <label htmlFor={id} className={styles.legend}>增益</label>
      <div className={styles.slider}>
        <input id={id} type="range" min={-30} max={0} step={0.1} value={shown} aria-valuetext={gainLabel(shown)} disabled={disabled} onChange={(event) => setDraft(Math.round(Number(event.target.value) * 10) / 10)} onPointerUp={commit} onKeyUp={commit} onBlur={commit} />
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

/** 设置 › 播放：音量均衡、无缝、交叉渐变、音质与 Headroom，作用于正在控制的播放区（服务端渲染）；还没读到播放区时禁用。 */
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
      <Switch label="Headroom" checked={settings.headroom.enabled} disabled={disabled} onChange={(enabled) => engine.setPlaybackSettings({ headroom: { ...settings.headroom, enabled } })} />
      <GainSlider value={settings.headroom.db} disabled={disabled || !settings.headroom.enabled} onCommit={(db) => engine.setPlaybackSettings({ headroom: { ...settings.headroom, db } })} />
      <Switch label="削波指示" checked={settings.headroom.clipIndicator} disabled={disabled} onChange={(clipIndicator) => engine.setPlaybackSettings({ headroom: { ...settings.headroom, clipIndicator } })} />
    </div>
  );
}
