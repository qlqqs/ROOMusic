import { ArrowRightLeft, MonitorSpeaker, Pencil, Trash2, X } from "lucide-react";
import { useId, useRef, useState, type CSSProperties, type FormEvent } from "react";
import { Button, ConfirmDialog, FailureNotice, ModalDialog, StatusTag, TextField } from "../../../shared/ui";
import { usePlayer, usePlayerSelector } from "../player-provider";
import type { PlaybackDeviceRow } from "../zones/controller";
import styles from "./device-picker.module.css";

const failureMessages = { zone_offline: "那台设备已离线。", device_online: "这台设备在线，不能删除。" } as const;
const MAX_NAME_LENGTH = 64;

/** 一行的第二行：离线、没有曲目、正在放的曲名或“已暂停”。 */
function stateText({ device, zone }: PlaybackDeviceRow): string {
  if (!device.online) return "离线";
  if (zone === null || zone.current === null) return "未播放";
  return zone.intent === "play" ? zone.current.track.title : "已暂停";
}

/**
 * “播放设备”按钮：点开设备列表。正在控制本机时只有图标；控制别的设备时图标加设备名，一眼看出命令发往哪里。
 * 播放栏与全屏底栏各一个。
 */
export function DeviceButton() {
  const remoteName = usePlayerSelector((snapshot) => (snapshot.isLocal || snapshot.device === null ? null : snapshot.device.name));
  const [anchor, setAnchor] = useState<DOMRect | null>(null);
  const open = (target: HTMLElement) => setAnchor(target.getBoundingClientRect());
  return <>
    {remoteName === null
      ? <Button variant="icon" icon={<MonitorSpeaker size={18} />} label="播放设备" onClick={(event) => open(event.currentTarget)} />
      : <span className={styles.remote}><Button icon={<MonitorSpeaker size={16} />} aria-label={`播放设备：${remoteName}`} title={remoteName} onClick={(event) => open(event.currentTarget)}><span className={styles.remoteName}>{remoteName}</span></Button></span>}
    <DeviceDialog anchor={anchor} onClose={() => setAnchor(null)} />
  </>;
}

/** 正在控制的设备名（图标 + 名字）；控制本机时不渲染。窄屏播放栏把它放在标题下，代替设备按钮里放不下的名字。 */
export function ControlTargetLine({ className }: { className?: string | undefined }) {
  const remoteName = usePlayerSelector((snapshot) => (snapshot.isLocal || snapshot.device === null ? null : snapshot.device.name));
  if (remoteName === null) return null;
  return <p className={`${styles.targetLine} ${className ?? ""}`}><MonitorSpeaker size={12} aria-hidden="true" /><span>{remoteName}</span></p>;
}

/** 设备列表弹层：桌面在按钮上方，窄屏从底部弹出。点一行即控制那台设备；行内可以转移、改名，离线的还能转到本机或删除。 */
function DeviceDialog({ anchor, onClose }: { anchor: DOMRect | null; onClose: () => void }) {
  const titleId = useId();
  const position = anchor === null ? undefined : ({ "--anchor-x": `${anchor.left + anchor.width / 2}px`, "--anchor-top": `${anchor.top}px` } as CSSProperties);
  return <ModalDialog open={anchor !== null} titleId={titleId} onRequestClose={onClose} closeOnBackdrop className={styles.dialog}>
    <div className={styles.content} style={position}><DeviceList titleId={titleId} onClose={onClose} /></div>
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
        const text = <><span className={styles.name}><span className={styles.nameText}>{device.name}</span>{row.isLocal ? <StatusTag>本机</StatusTag> : null}</span><span className={styles.state}>{stateText(row)}</span></>;
        // 转到这台设备：把正在控制的播放区转过来；离线的设备可以转到本机接着放。
        const transferHere = device.online && !row.isTarget && source !== null && source.current !== null ? source.zoneId : null;
        const transferToLocal = !device.online && zone !== null && zone.current !== null && localZoneId !== undefined ? zone.zoneId : null;
        return <li key={device.deviceId} className={styles.row} aria-current={row.isTarget ? "true" : undefined}>
          {device.online
            ? <button type="button" className={styles.select} aria-pressed={row.isTarget} onClick={() => { engine.setControlTarget(device.zoneId); onClose(); }}>{text}</button>
            : <div className={styles.select}>{text}</div>}
          <div className={styles.actions} role="group" aria-label={`${device.name}的操作`}>
            {transferHere === null ? null : <Button variant="icon" icon={<ArrowRightLeft size={16} />} label="转到这台设备" onClick={() => transfer(transferHere, device.zoneId)} />}
            {transferToLocal === null || localZoneId === undefined ? null : <Button variant="icon" icon={<ArrowRightLeft size={16} />} label="转到本机" onClick={() => transfer(transferToLocal, localZoneId)} />}
            <Button variant="icon" icon={<Pencil size={16} />} label="改名" onClick={() => setRenaming(row)} />
            {device.online ? null : <Button variant="icon" icon={<Trash2 size={16} />} label="删除" onClick={() => { setDeleteError(null); setDeleting(row); }} />}
          </div>
        </li>;
      })}
    </ul>
    <RenameDialog row={renaming} onClose={() => setRenaming(null)} />
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
