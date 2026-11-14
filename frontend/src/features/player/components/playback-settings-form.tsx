import { useRef, useState } from "react";
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
import { IrFilesDialog } from "../convolution/ir-files-dialog";
import { useIrFiles } from "../convolution/hooks";
import { PeqEditorDialog } from "../peq/peq-editor";
import { usePlayerEngine, usePlayerSelector } from "../player-provider";
import { Button, Segmented, SettingRow, SettingsGroup, Switch, VisuallyHidden } from "../../../shared/ui";
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

/** 下拉选择：选中即提交。名称来自所在设置行的 label（id 由行给出）。 */
function Select<T extends string | number>({ id, describedBy, options, value, disabled, onChange }: { id: string; describedBy?: string | undefined; options: readonly Option<T>[]; value: T; disabled: boolean; onChange: (value: T) => void }) {
  return (
    <select id={id} aria-describedby={describedBy} className={styles.select} value={value} disabled={disabled} onChange={(event) => {
      const picked = options.find((option) => String(option.value) === event.target.value);
      if (picked !== undefined) onChange(picked.value);
    }}>
      {options.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
    </select>
  );
}

/** Headroom 增益滑块：拖动时只改本地显示，松手、按键抬起或离开时才发出，同一个草稿值只发一次；服务端的值变了就跟上服务端。 */
function GainSlider({ id, value, disabled, onCommit }: { id: string; value: number; disabled: boolean; onCommit: (db: number) => void }) {
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
    <div className={styles.slider}>
      <input id={id} type="range" min={-30} max={0} step={0.1} value={shown} aria-valuetext={gainLabel(shown)} disabled={disabled} onChange={(event) => setDraft({ db: Math.round(Number(event.target.value) * 10) / 10 })} onPointerUp={commit} onKeyUp={commit} onBlur={commit} />
      <output htmlFor={id} className={styles.sliderValue}>{gainLabel(shown)}</output>
    </div>
  );
}

/** 参数均衡：“编辑…”打开编辑面板，开关在行尾。 */
function PeqRow({ disabled }: { disabled: boolean }) {
  const engine = usePlayerEngine();
  const peq = usePlayerSelector((snapshot) => snapshot.settings.peq);
  const [editing, setEditing] = useState(false);
  return (
    <SettingRow label="参数均衡">
      {(ids) => <>
        <Button disabled={disabled} onClick={() => setEditing(true)}><span>编辑<VisuallyHidden>参数均衡</VisuallyHidden>…</span></Button>
        <Switch id={ids.controlId} checked={peq.enabled} disabled={disabled} onChange={(enabled) => engine.setPlaybackSettings({ peq: { ...peq, enabled } })} />
        <PeqEditorDialog open={editing} onClose={() => setEditing(false)} />
      </>}
    </SettingRow>
  );
}

/** 卷积：选 IR、“管理文件…”打开 IR 文件管理，开关在行尾；选中一个 IR 就打开卷积。 */
function ConvolutionRow({ disabled }: { disabled: boolean }) {
  const engine = usePlayerEngine();
  const convolution = usePlayerSelector((snapshot) => snapshot.settings.convolution);
  const files = useIrFiles();
  const [managing, setManaging] = useState(false);
  return (
    <SettingRow label="卷积">
      {(ids) => <>
        <select aria-label="IR 文件" className={styles.select} value={convolution.irId ?? ""} disabled={disabled || files.data === undefined} onChange={(event) => {
          if (event.target.value !== "") engine.setPlaybackSettings({ convolution: { ...convolution, irId: event.target.value, enabled: true } });
        }}>
          {convolution.irId === null ? <option value="">选择 IR</option> : null}
          {files.data?.map((file) => <option key={file.irId} value={file.irId}>{file.name}</option>)}
        </select>
        <Button disabled={disabled} onClick={() => setManaging(true)}><span>管理<VisuallyHidden> IR </VisuallyHidden>文件…</span></Button>
        <Switch id={ids.controlId} checked={convolution.enabled} disabled={disabled} onChange={(enabled) => engine.setPlaybackSettings({ convolution: { ...convolution, enabled } })} />
        <IrFilesDialog open={managing} onClose={() => setManaging(false)} />
      </>}
    </SettingRow>
  );
}

/** 参数均衡与卷积都开着时的先后：两步排成一条链，“对调”交换先后。 */
function DspOrder({ disabled }: { disabled: boolean }) {
  const engine = usePlayerEngine();
  const convolution = usePlayerSelector((snapshot) => snapshot.settings.convolution);
  const peqEnabled = usePlayerSelector((snapshot) => snapshot.settings.peq.enabled);
  if (!peqEnabled || !convolution.enabled || convolution.irId === null) return null;
  const order = convolution.beforePeq ? ["卷积", "参数均衡"] : ["参数均衡", "卷积"];
  return (
    <SettingRow label="处理顺序" plainLabel>
      {(ids) => <>
        <ol role="list" className={styles.chain} aria-labelledby={ids.labelId}>
          {order.map((step) => <li key={step}><span className={styles.node}>{step}</span></li>)}
        </ol>
        <Button disabled={disabled} onClick={() => engine.setPlaybackSettings({ convolution: { ...convolution, beforePeq: !convolution.beforePeq } })}><span>对调<VisuallyHidden>处理顺序</VisuallyHidden></span></Button>
      </>}
    </SettingRow>
  );
}

/** 设置页页首的一句话：这些设置改的是正在控制的那台设备；还没有设备时不渲染。 */
export function PlaybackSettingsDevice() {
  const name = usePlayerSelector((snapshot) => snapshot.device?.name ?? null);
  if (name === null) return null;
  return <>这些设置作用于正在控制的设备：<span className={styles.deviceName}>{name}</span></>;
}

/**
 * 设置 › 播放：分“播放”“输出”“音效处理”三组，作用于正在控制的播放区（服务端渲染）；还没读到播放区时禁用。
 * 从属项（渐变时长、目标采样率、增益）缩进在所属开关或选项下，所属项关着时整行不出现。
 */
export function PlaybackSettingsForm() {
  const engine = usePlayerEngine();
  const settings = usePlayerSelector((snapshot) => snapshot.settings);
  const disabled = usePlayerSelector((snapshot) => snapshot.zone === null);
  return (
    <div className={styles.form}>
      <SettingsGroup title="播放">
        <SettingRow label="串流音质" description="省流量时转成有损格式传输">
          {(ids) => <Segmented<StreamQuality>
            {...ids}
            options={streamQualities.map((quality) => ({ value: quality, label: qualityLabels[quality] }))}
            value={settings.quality}
            disabled={disabled}
            onChange={(quality) => engine.setPlaybackSettings({ quality })}
          />}
        </SettingRow>
        <SettingRow label="无缝播放" description="曲目之间不留空隙">
          {(ids) => <Switch id={ids.controlId} aria-describedby={ids.descriptionId} checked={settings.gapless} disabled={disabled} onChange={(gapless) => engine.setPlaybackSettings({ gapless })} />}
        </SettingRow>
        <SettingRow label="交叉渐变" description="上一首淡出时下一首淡入">
          {(ids) => <Switch id={ids.controlId} aria-describedby={ids.descriptionId} checked={settings.crossfade} disabled={disabled} onChange={(crossfade) => engine.setPlaybackSettings({ crossfade })} />}
        </SettingRow>
        {settings.crossfade ? (
          <SettingRow label="渐变时长" nested>
            {(ids) => <Segmented<CrossfadeSeconds>
              {...ids}
              options={crossfadeDurations.map((seconds) => ({ value: seconds, label: `${seconds} 秒` }))}
              value={settings.crossfadeSeconds}
              disabled={disabled}
              onChange={(crossfadeSeconds) => engine.setPlaybackSettings({ crossfadeSeconds })}
            />}
          </SettingRow>
        ) : null}
        <SettingRow label="音量均衡">
          {(ids) => <Segmented<LevelingMode>
            {...ids}
            options={levelingModes.map((mode) => ({ value: mode, label: levelingLabels[mode] }))}
            value={settings.leveling}
            disabled={disabled}
            onChange={(leveling) => engine.setPlaybackSettings({ leveling })}
          />}
        </SettingRow>
      </SettingsGroup>
      <SettingsGroup title="输出">
        <SettingRow label="采样率转换">
          {(ids) => <Select<SampleRateStrategy>
            id={ids.controlId}
            options={sampleRateStrategies.map((strategy) => ({ value: strategy, label: strategyLabels[strategy] }))}
            value={settings.sampleRate.strategy}
            disabled={disabled}
            onChange={(strategy) => engine.setPlaybackSettings({ sampleRate: { ...settings.sampleRate, strategy, customRate: strategy === "custom" ? (settings.sampleRate.customRate ?? DEFAULT_CUSTOM_RATE) : settings.sampleRate.customRate } })}
          />}
        </SettingRow>
        {settings.sampleRate.strategy === "custom" ? (
          <SettingRow label="目标采样率" nested>
            {(ids) => <Select<number>
              id={ids.controlId}
              options={customSampleRates.map((rate) => ({ value: rate, label: `${rate / 1000} kHz` }))}
              value={settings.sampleRate.customRate ?? DEFAULT_CUSTOM_RATE}
              disabled={disabled}
              onChange={(customRate) => engine.setPlaybackSettings({ sampleRate: { ...settings.sampleRate, customRate } })}
            />}
          </SettingRow>
        ) : null}
        <SettingRow label="转换滤波器">
          {(ids) => <Select<SampleRateFilter>
            id={ids.controlId}
            options={sampleRateFilters.map((filter) => ({ value: filter, label: filterLabels[filter] }))}
            value={settings.sampleRate.filter}
            disabled={disabled}
            onChange={(filter) => engine.setPlaybackSettings({ sampleRate: { ...settings.sampleRate, filter } })}
          />}
        </SettingRow>
        <SettingRow label="多声道降为双声道">
          {(ids) => <Switch id={ids.controlId} checked={settings.downmix.enabled} disabled={disabled} onChange={(enabled) => engine.setPlaybackSettings({ downmix: { enabled } })} />}
        </SettingRow>
      </SettingsGroup>
      <SettingsGroup title="音效处理">
        <PeqRow disabled={disabled} />
        <ConvolutionRow disabled={disabled} />
        <DspOrder disabled={disabled} />
        <SettingRow label="Headroom" description="整体降低电平，给均衡与卷积留出余量">
          {(ids) => <Switch id={ids.controlId} aria-describedby={ids.descriptionId} checked={settings.headroom.enabled} disabled={disabled} onChange={(enabled) => engine.setPlaybackSettings({ headroom: { ...settings.headroom, enabled } })} />}
        </SettingRow>
        {settings.headroom.enabled ? (
          <SettingRow label="增益" nested>
            {(ids) => <GainSlider id={ids.controlId} value={settings.headroom.db} disabled={disabled} onCommit={(db) => engine.setPlaybackSettings({ headroom: { ...settings.headroom, db } })} />}
          </SettingRow>
        ) : null}
        {/* 削波检测不依赖 Headroom（服务端只看这个开关），所以不是 Headroom 的从属项：Headroom 关着时也能开关它。 */}
        <SettingRow label="削波指示" description="出现削波时在信号路径里提示">
          {(ids) => <Switch id={ids.controlId} aria-describedby={ids.descriptionId} checked={settings.headroom.clipIndicator} disabled={disabled} onChange={(clipIndicator) => engine.setPlaybackSettings({ headroom: { ...settings.headroom, clipIndicator } })} />}
        </SettingRow>
      </SettingsGroup>
    </div>
  );
}
