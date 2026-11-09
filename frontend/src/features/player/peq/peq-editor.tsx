import { BookmarkCheck, ClipboardPaste, Headphones, Plus, Trash2, X } from "lucide-react";
import { useId, useRef, useState } from "react";
import { Button, ModalDialog } from "../../../shared/ui";
import { usePlayerEngine, usePlayerSelector } from "../player-provider";
import { bandHasGain, bandHasSlope, newBand, peqBandTypes, peqLimits, peqSlopes, PEQ_MAX_BANDS, samePeq, sourceLabel, withType, type PeqBand, type PeqBandType, type PeqChannels, type PeqDocument } from "./document";
import { usePeqResponse } from "./hooks";
import { OpraDialog } from "./opra-dialog";
import { PeqCurve } from "./peq-curve";
import { PeqImportDialog } from "./peq-import-dialog";
import { PeqPresetsDialog } from "./peq-presets-dialog";
import styles from "./peq.module.css";

const typeLabels: Record<PeqBandType, string> = {
  peaking: "峰值",
  low_shelf: "低架",
  high_shelf: "高架",
  low_pass: "低通",
  high_pass: "高通",
  band_pass: "带通",
  notch: "陷波",
  all_pass: "全通",
};

type Range = { min: number; max: number };

/** 数字输入：输入时只预览，回车或离开时才提交；输入不成数字时恢复原值。 */
function NumberInput({ label, value, range, step, unit, onPreview, onCommit }: { label: string; value: number; range: Range; step: number; unit: string; onPreview: (value: number) => void; onCommit: (value: number) => void }) {
  const [text, setText] = useState(String(value));
  const [seen, setSeen] = useState(value);
  // 回车后紧接失焦是同一次确认：服务端值还没回来时，同一个数不再提交第二次。
  const committed = useRef<{ base: number; number: number } | null>(null);
  if (value !== seen) {
    setSeen(value);
    setText(String(value));
  }
  const parsed = (raw: string) => {
    const number = Number(raw);
    return raw.trim() === "" || !Number.isFinite(number) ? null : Math.min(range.max, Math.max(range.min, number));
  };
  const commit = () => {
    const number = parsed(text);
    if (number === null) {
      setText(String(value));
      return;
    }
    setText(String(number));
    if (committed.current?.base === value && committed.current.number === number) return;
    committed.current = { base: value, number };
    onCommit(number);
  };
  return (
    <span className={styles.number}>
      <input
        type="number"
        inputMode="decimal"
        aria-label={label}
        value={text}
        min={range.min}
        max={range.max}
        step={step}
        onChange={(event) => {
          setText(event.target.value);
          const number = parsed(event.target.value);
          if (number !== null) onPreview(number);
        }}
        onBlur={commit}
        onKeyDown={(event) => {
          if (event.key === "Enter") commit();
        }}
      />
      <span className={styles.unit} aria-hidden="true">{unit}</span>
    </span>
  );
}

function channelsKey(channels: PeqChannels): string {
  return channels === "all" ? "all" : channels.join(",");
}

function channelOptions(current: PeqChannels): { key: string; label: string; channels: PeqChannels }[] {
  const options: { key: string; label: string; channels: PeqChannels }[] = [
    { key: "all", label: "全部", channels: "all" },
    { key: "0", label: "左", channels: [0] },
    { key: "1", label: "右", channels: [1] },
  ];
  if (!options.some((option) => option.key === channelsKey(current)) && current !== "all") options.push({ key: channelsKey(current), label: current.map((channel) => `声道 ${channel + 1}`).join("、"), channels: current });
  return options;
}

type Change = (next: PeqDocument, commit: boolean) => void;

function BandRow({ band, index, draft, onChange }: { band: PeqBand; index: number; draft: PeqDocument; onChange: Change }) {
  const name = `第 ${index + 1} 段`;
  const update = (patch: Partial<PeqBand>, commit: boolean) => onChange({ ...draft, bands: draft.bands.map((item, at) => (at === index ? { ...item, ...patch } : item)) }, commit);
  const options = channelOptions(band.channels);
  return (
    <li className={styles.band} data-off={band.enabled ? undefined : ""}>
      <input type="checkbox" className={styles.check} aria-label={`启用${name}`} checked={band.enabled} onChange={(event) => update({ enabled: event.target.checked }, true)} />
      <select className={styles.select} aria-label={`${name}类型`} value={band.type} onChange={(event) => onChange({ ...draft, bands: draft.bands.map((item, at) => (at === index ? withType(item, event.target.value as PeqBandType) : item)) }, true)}>
        {peqBandTypes.map((type) => <option key={type} value={type}>{typeLabels[type]}</option>)}
      </select>
      <NumberInput label={`${name}频率`} value={band.freqHz} range={peqLimits.freqHz} step={1} unit="Hz" onPreview={(freqHz) => update({ freqHz }, false)} onCommit={(freqHz) => update({ freqHz }, true)} />
      {bandHasGain(band.type) ? (
        <NumberInput label={`${name}增益`} value={band.gainDb ?? 0} range={peqLimits.gainDb} step={0.1} unit="dB" onPreview={(gainDb) => update({ gainDb }, false)} onCommit={(gainDb) => update({ gainDb }, true)} />
      ) : <span className={styles.empty} />}
      {bandHasSlope(band.type) ? (
        <select className={styles.select} aria-label={`${name}斜率`} value={band.slopeDb ?? 12} onChange={(event) => update({ slopeDb: Number(event.target.value) }, true)}>
          {peqSlopes.map((slope) => <option key={slope} value={slope}>{slope} dB/oct</option>)}
        </select>
      ) : (
        <NumberInput label={`${name} Q`} value={band.q ?? 0.71} range={peqLimits.q} step={0.01} unit="Q" onPreview={(q) => update({ q }, false)} onCommit={(q) => update({ q }, true)} />
      )}
      <select className={styles.select} aria-label={`${name}声道`} value={channelsKey(band.channels)} onChange={(event) => update({ channels: options.find((option) => option.key === event.target.value)?.channels ?? "all" }, true)}>
        {options.map((option) => <option key={option.key} value={option.key}>{option.label}</option>)}
      </select>
      <Button variant="icon" icon={<Trash2 size={16} />} label={`删除${name}`} onClick={() => onChange({ ...draft, bands: draft.bands.filter((_, at) => at !== index) }, true)} />
    </li>
  );
}

/** PEQ 编辑面板：改动先进本地草稿并预览曲线，一次完成的操作提交一次完整文档。 */
function PeqEditorBody({ titleId, onClose }: { titleId: string; onClose: () => void }) {
  const engine = usePlayerEngine();
  const server = usePlayerSelector((snapshot) => snapshot.settings.peq);
  const [draft, setDraft] = useState(server);
  const [seen, setSeen] = useState(server);
  if (!samePeq(server, seen)) {
    setSeen(server);
    setDraft(server);
  }
  const [panel, setPanel] = useState<"opra" | "import" | "presets" | null>(null);
  const response = usePeqResponse(draft);
  const change: Change = (next, commit) => {
    setDraft(next);
    if (commit && !samePeq(next, server)) engine.setPlaybackSettings({ peq: next });
  };
  const apply = (peq: PeqDocument) => {
    change({ ...peq, enabled: true }, true);
    setPanel(null);
  };
  const label = sourceLabel(draft.source);
  return (
    <div className={styles.editor}>
      <div className={styles.header}>
        <div className={styles.heading}>
          <h2 id={titleId} className={styles.title}>参数均衡</h2>
          {label === null ? null : (
            <p className={styles.source}>
              {draft.source?.kind === "opra" && draft.source.link !== "" ? <>{draft.source.product} · <a href={draft.source.link} target="_blank" rel="noreferrer">{draft.source.author}</a></> : label}
            </p>
          )}
        </div>
        <div className={styles.toolbar}>
          <Button variant="icon" icon={<Headphones size={18} />} label="耳机校正" onClick={() => setPanel("opra")} />
          <Button variant="icon" icon={<ClipboardPaste size={18} />} label="粘贴导入" onClick={() => setPanel("import")} />
          <Button variant="icon" icon={<BookmarkCheck size={18} />} label="预设" onClick={() => setPanel("presets")} />
          <Button variant="icon" icon={<X size={18} />} label="关闭" onClick={onClose} />
        </div>
      </div>
      <label className={styles.preamp}>
        <span className={styles.label}>前级增益</span>
        <NumberInput label="前级增益" value={draft.preampDb} range={peqLimits.preampDb} step={0.1} unit="dB" onPreview={(preampDb) => change({ ...draft, preampDb }, false)} onCommit={(preampDb) => change({ ...draft, preampDb }, true)} />
      </label>
      <PeqCurve curves={response.data ?? []} />
      <ol className={styles.bands}>
        {draft.bands.map((band, index) => <BandRow key={index} band={band} index={index} draft={draft} onChange={change} />)}
      </ol>
      <div>
        <Button variant="icon" icon={<Plus size={18} />} label="添加一段" disabled={draft.bands.length >= PEQ_MAX_BANDS} onClick={() => change({ ...draft, bands: [...draft.bands, newBand] }, true)} />
      </div>
      <OpraDialog open={panel === "opra"} onApply={apply} onClose={() => setPanel(null)} />
      <PeqImportDialog open={panel === "import"} onApply={apply} onClose={() => setPanel(null)} />
      <PeqPresetsDialog open={panel === "presets"} current={draft} onApply={apply} onClose={() => setPanel(null)} />
    </div>
  );
}

export function PeqEditorDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const titleId = useId();
  return (
    <ModalDialog open={open} titleId={titleId} className={styles.dialog} onRequestClose={onClose}>
      <PeqEditorBody titleId={titleId} onClose={onClose} />
    </ModalDialog>
  );
}
