import { ArrowRightLeft, AudioWaveform, MonitorSpeaker, Pencil, Speaker, Trash2, X } from "lucide-react";
import { useId, useRef, useState, type CSSProperties, type FormEvent } from "react";
import { failureOf, isHttpFailure } from "../../../shared/api";
import { cx } from "../../../shared/lib/class-names";
import { Button, ConfirmDialog, FailureNotice, ModalDialog, StatusTag, TextField } from "../../../shared/ui";
import { usePlayer, usePlayerSelector } from "../player-provider";
import type { PlaybackDeviceRow } from "../zones/controller";
import { UPNP_SAMPLE_RATES } from "../zones/model";
import { rejectionLabels } from "./player-labels";
import styles from "./device-picker.module.css";

const failureMessages = { zone_offline: rejectionLabels.zone_offline, device_online: "这台设备在线，不能删除。" } as const;
const MAX_NAME_LENGTH = 64;

/** 一行的第二行：离线、别人正在用（局域网设备）、没有曲目、正在放的曲名或“已暂停”。 */
function stateText(row: PlaybackDeviceRow): string {
  const { device, zone } = row;
  if (!device.online) return "离线";
  if (row.occupant !== null) return `${row.occupant} 正在使用`;
  if (zone === null || zone.current === null) return "未播放";
  return zone.intent === "play" ? zone.current.track.title : "已暂停";
}

/**
 * “播放设备”按钮：只有图标，点开设备列表。控制本机时是普通图标；控制别的设备时图标用强调色、右上角一个小圆点，
 * 读屏名与悬停提示写“正在控制：设备名”（不只靠颜色）。窄屏迷你栏由 PlayerBar 决定入口是否显示。
 */
export function DeviceButton({ className }: { className?: string | undefined }) {
  const remoteName = usePlayerSelector((snapshot) => (snapshot.isLocal || snapshot.device === null ? null : snapshot.device.name));
  const [anchor, setAnchor] = useState<DOMRect | null>(null);
  return <span className={cx(styles.device, className)} data-remote={remoteName === null ? undefined : ""}>
    <Button variant="icon" icon={<MonitorSpeaker size={18} />} label={remoteName === null ? "播放设备" : `正在控制：${remoteName}`} onClick={(event) => setAnchor(event.currentTarget.getBoundingClientRect())} />
    <DeviceDialog anchor={anchor} onClose={() => setAnchor(null)} />
  </span>;
}

/**
 * 设备列表弹层：桌面在按钮上方（播放栏）；按钮在视口上半部时（全屏播放器右上角）改在按钮下方。窄屏从底部弹出。
 * 点一行即控制那台设备；行内可以转移、改名，离线的还能转到本机或删除。
 */
function DeviceDialog({ anchor, onClose }: { anchor: DOMRect | null; onClose: () => void }) {
  const titleId = useId();
  const position = anchor === null ? undefined : ({ "--anchor-x": `${anchor.left + anchor.width / 2}px`, "--anchor-top": `${anchor.top}px`, "--anchor-bottom": `${anchor.bottom}px` } as CSSProperties);
  const below = anchor !== null && anchor.top < window.innerHeight / 2;
  return <ModalDialog open={anchor !== null} titleId={titleId} onRequestClose={onClose} closeOnBackdrop className={styles.dialog}>
    <div className={styles.content} style={position} data-below={below ? "" : undefined}><DeviceList titleId={titleId} onClose={onClose} /></div>
  </ModalDialog>;
}

function DeviceList({ titleId, onClose }: { titleId: string; onClose: () => void }) {
  const { engine, snapshot } = usePlayer();
  const heading = useRef<HTMLHeadingElement>(null);
  const [error, setError] = useState<unknown>(null);
  const [renaming, setRenaming] = useState<PlaybackDeviceRow | null>(null);
  const [deleting, setDeleting] = useState<PlaybackDeviceRow | null>(null);
  const [deletePending, setDeletePending] = useState(false);
  const [deleteError, setDeleteError] = useState<unknown>(null);
  const source = snapshot.zone;
  const localZoneId = snapshot.devices.find((row) => row.isLocal)?.device.zoneId;
  // 局域网设备要先“激活”（服务器为它认领输出）；别人在用时先确认接管。then 是激活之后要做的事（控制它，或把播放转过去）。
  const [takeover, setTakeover] = useState<{ row: PlaybackDeviceRow; who: string; then: () => Promise<void> } | null>(null);
  const [takeoverPending, setTakeoverPending] = useState(false);
  const [rateRow, setRateRow] = useState<PlaybackDeviceRow | null>(null);

  function activate(row: PlaybackDeviceRow, then: () => Promise<void>) {
    setError(null);
    if (row.occupant !== null) {
      setTakeover({ row, who: `${row.occupant} 正在使用这台设备`, then });
      return;
    }
    engine.activateDevice(row.device.deviceId, false).then(then).then(onClose, (failure: unknown) => {
      // 列表刷新前别人刚开始用：改为确认接管。
      const busy = failureOf(failure);
      if (isHttpFailure(busy, 409, "device_busy")) setTakeover({ row, who: busy?.kind === "http" ? busy.message : "其他成员正在使用这台设备", then });
      else setError(failure);
    });
  }
  function confirmTakeover() {
    if (takeover === null) return;
    setTakeoverPending(true);
    engine.activateDevice(takeover.row.device.deviceId, true).then(takeover.then).then(() => {
      setTakeover(null);
      onClose();
    }, (failure: unknown) => {
      setTakeover(null);
      setError(failure);
    }).finally(() => setTakeoverPending(false));
  }
  function select(row: PlaybackDeviceRow) {
    if (row.device.lan !== null) {
      activate(row, async () => {});
      return;
    }
    engine.setControlTarget(row.device.zoneId);
    onClose();
  }
  function transferTo(row: PlaybackDeviceRow, sourceZoneId: string) {
    if (row.device.lan !== null) {
      activate(row, () => engine.transfer(sourceZoneId, row.device.zoneId));
      return;
    }
    transfer(sourceZoneId, row.device.zoneId);
  }

  function transfer(sourceZoneId: string, targetZoneId: string) {
    setError(null);
    engine.transfer(sourceZoneId, targetZoneId).then(onClose, setError);
  }
  function confirmDelete() {
    if (deleting === null) return;
    setDeletePending(true);
    setDeleteError(null);
    engine.deleteDevice(deleting.device.deviceId).then(() => {
      setDeleting(null);
      queueMicrotask(() => heading.current?.focus());
    }, setDeleteError).finally(() => setDeletePending(false));
  }

  return <>
    <div className={styles.header}>
      <h2 ref={heading} id={titleId} tabIndex={-1} className={styles.title}>播放设备</h2>
      <Button variant="icon" icon={<X size={16} />} label="关闭" onClick={onClose} />
    </div>
    {error === null ? null : <FailureNotice error={error} messages={failureMessages} />}
    <ul role="list" className={styles.list}>
      {snapshot.devices.map((row) => {
        const { device, zone } = row;
        const text = <><span className={styles.name}>{device.lan === null ? null : <Speaker size={14} aria-hidden="true" className={styles.kindIcon} />}<span className={styles.nameText}>{device.name}</span>{row.isLocal ? <StatusTag>本机</StatusTag> : null}{device.lan === null ? null : <StatusTag>{device.kind === "cast" ? "Google Cast" : "局域网"}</StatusTag>}</span><span className={styles.state}>{stateText(row)}</span></>;
        // 转到这台设备：把正在控制的播放区转过来；离线的设备可以转到本机接着放。
        const transferHere = device.online && !row.isTarget && source !== null && source.current !== null ? source.zoneId : null;
        const transferToLocal = !device.online && zone !== null && zone.current !== null && localZoneId !== undefined ? zone.zoneId : null;
        return <li key={device.deviceId} className={styles.row} aria-current={row.isTarget ? "true" : undefined}>
          {device.online
            ? <button type="button" className={styles.select} aria-pressed={row.isTarget} onClick={() => select(row)}>{text}</button>
            : <div className={styles.select}>{text}</div>}
          <div className={styles.actions} role="group" aria-label={`${device.name}的操作`}>
            {transferHere === null ? null : <Button variant="icon" icon={<ArrowRightLeft size={16} />} label="转到这台设备" onClick={() => transferTo(row, transferHere)} />}
            {transferToLocal === null || localZoneId === undefined ? null : <Button variant="icon" icon={<ArrowRightLeft size={16} />} label="转到本机" onClick={() => transfer(transferToLocal, localZoneId)} />}
            {/* Google Cast 的格式与采样率由管理员在“局域网设备”里按设备设置。 */}
            {device.lan === null || device.kind === "cast" ? null : <Button variant="icon" icon={<AudioWaveform size={16} />} label="采样率上限" onClick={() => setRateRow(row)} />}
            <Button variant="icon" icon={<Pencil size={16} />} label="改名" onClick={() => setRenaming(row)} />
            {device.online ? null : <Button variant="icon" icon={<Trash2 size={16} />} label="删除" onClick={() => { setDeleteError(null); setDeleting(row); }} />}
          </div>
        </li>;
      })}
    </ul>
    <RenameDialog row={renaming} onClose={() => setRenaming(null)} />
    <ConfirmDialog
      open={takeover !== null}
      title={`接管“${takeover?.row.device.name ?? ""}”？`}
      confirmLabel="接管"
      pending={takeoverPending}
      onCancel={() => setTakeover(null)}
      onConfirm={confirmTakeover}
    ><p>{takeover?.who}。接管后对方的播放会暂停。</p></ConfirmDialog>
    {rateRow === null ? null : <SampleRateDialog key={rateRow.device.deviceId} row={rateRow} onClose={() => setRateRow(null)} />}
    <ConfirmDialog
      open={deleting !== null}
      title={`删除“${deleting?.device.name ?? ""}”？`}
      confirmLabel="删除"
      pending={deletePending}
      error={deleteError === null ? undefined : <FailureNotice error={deleteError} messages={failureMessages} />}
      onCancel={() => setDeleting(null)}
      onConfirm={confirmDelete}
    ><p>它的队列会一起删除。</p></ConfirmDialog>
  </>;
}

function RenameDialog({ row, onClose }: { row: PlaybackDeviceRow | null; onClose: () => void }) {
  const titleId = useId();
  return <ModalDialog open={row !== null} titleId={titleId} onRequestClose={onClose} className={styles.renameDialog}>
    {row === null ? null : <RenameForm key={row.device.deviceId} row={row} titleId={titleId} onClose={onClose} />}
  </ModalDialog>;
}

function RenameForm({ row, titleId, onClose }: { row: PlaybackDeviceRow; titleId: string; onClose: () => void }) {
  const { engine } = usePlayer();
  const [name, setName] = useState(row.device.name);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const trimmed = name.trim();
  function submit(event: FormEvent) {
    event.preventDefault();
    if (pending || trimmed === "") return;
    if (trimmed === row.device.name) {
      onClose();
      return;
    }
    setPending(true);
    setError(null);
    engine.renameDevice(row.device.deviceId, trimmed).then(onClose, (failure: unknown) => {
      setError(failure);
      setPending(false);
    });
  }
  return <form className={styles.renameForm} onSubmit={submit}>
    <h2 id={titleId} className={styles.title}>改名</h2>
    <TextField label="设备名称" value={name} maxLength={MAX_NAME_LENGTH} autoFocus onChange={(event) => setName(event.target.value)} />
    {error === null ? null : <FailureNotice error={error} />}
    <div className={styles.formActions}>
      <Button onClick={onClose} disabled={pending}>取消</Button>
      <Button type="submit" variant="primary" busy={pending} disabled={trimmed === ""}>保存</Button>
    </div>
  </form>;
}

/** 局域网设备的采样率上限：设备声明不了自己支持多高的采样率，默认 48 kHz，设备支持更高时可以调高。 */
function SampleRateDialog({ row, onClose }: { row: PlaybackDeviceRow; onClose: () => void }) {
  const { engine } = usePlayer();
  const current = row.device.lan?.maxSampleRate ?? 48000;
  const [rate, setRate] = useState<number>(current);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const name = useId();
  function save() {
    if (rate === current) {
      onClose();
      return;
    }
    setPending(true);
    setError(null);
    engine.setDeviceSampleRate(row.device.deviceId, rate).then(onClose, (failure: unknown) => {
      setError(failure);
      setPending(false);
    });
  }
  return <ConfirmDialog open title={`“${row.device.name}”的采样率上限`} confirmLabel="保存" pending={pending} error={error === null ? undefined : <FailureNotice error={error} />} onCancel={onClose} onConfirm={save}>
    <p id={`${name}-note`} className={styles.ratesNote}>高于上限的音乐会转换到上限再送给设备；下一首起生效。{row.device.lan?.formatDeclared === false ? "这台设备没有说明它支持的格式。" : ""}</p>
    <fieldset className={styles.rates} aria-describedby={`${name}-note`}>
      <legend className={styles.visuallyHidden}>采样率上限</legend>
      {UPNP_SAMPLE_RATES.map((value) => <label key={value} className={styles.rate}>
        <input type="radio" name={name} value={value} checked={rate === value} onChange={() => setRate(value)} />
        {(value / 1000).toLocaleString("zh-CN", { maximumFractionDigits: 1 })} kHz
      </label>)}
    </fieldset>
  </ConfirmDialog>;
}
