import { useId } from "react";
import { crossfadeDurations, levelingModes, type CrossfadeSeconds, type LevelingMode } from "../zones/model";
import { usePlayerEngine, usePlayerSelector } from "../player-provider";
import styles from "./playback-settings-form.module.css";

const levelingLabels: Record<LevelingMode, string> = { off: "关", auto: "自动", track: "按曲目", album: "按专辑" };

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

/** 设置 › 播放：音量均衡与交叉渐变，作用于正在控制的播放区（服务端渲染）；还没读到播放区时禁用。 */
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
      <label className={styles.switchRow}>
        <span className={styles.legend}>交叉渐变</span>
        <input type="checkbox" role="switch" className={styles.switch} checked={settings.crossfade} disabled={disabled} onChange={(event) => engine.setPlaybackSettings({ crossfade: event.target.checked })} />
      </label>
      <Segmented<CrossfadeSeconds>
        legend="渐变时长"
        options={crossfadeDurations.map((seconds) => ({ value: seconds, label: `${seconds} 秒` }))}
        value={settings.crossfadeSeconds}
        disabled={disabled || !settings.crossfade}
        onChange={(crossfadeSeconds) => engine.setPlaybackSettings({ crossfadeSeconds })}
      />
    </div>
  );
}
