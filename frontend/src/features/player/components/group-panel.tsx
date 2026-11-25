import { AudioLines, Clock, Crown, LoaderCircle, Lock, Plus, Timer, Unlink, Unplug, Volume1, VolumeX, WifiOff, X, type LucideIcon } from "lucide-react";
import { useId, useState, type ReactNode } from "react";
import { failureOf, isHttpFailure } from "../../../shared/api";
import { Button, ConfirmDialog, FailureNotice, ModalDialog, useTooltip } from "../../../shared/ui";
import { usePlayer } from "../player-provider";
import type { PlaybackDeviceRow } from "../zones/controller";
import type { Device, GroupMemberState } from "../zones/model";
import { groupMemberStateLabels } from "./player-labels";
import styles from "./group-panel.module.css";

/** 每组最多 8 台（含主设备），与服务端一致。 */
const MAX_GROUP_DEVICES = 8;
const SYNC_DELAY_LIMIT = 500;

const failureMessages = {
  group_full: "一组最多 8 台设备。",
  device_grouped: "这台设备已在组里。",
  group_unsupported: "先选用这台设备，再加入其他设备。",
  zone_offline: "主设备已离线。",
} as const;

const stateIcons: Record<GroupMemberState, LucideIcon> = { active: AudioLines, joining: LoaderCircle, waiting: Clock, dropped: Unplug, offline: WifiOff, busy: Lock };

/** 编组面板：leaderDeviceId 是主设备；为 null 时关闭。 */
export function GroupDialog({ leaderDeviceId, onClose }: { leaderDeviceId: string | null; onClose: () => void }) {
  const titleId = useId();
  return <ModalDialog open={leaderDeviceId !== null} titleId={titleId} onRequestClose={onClose} className={styles.dialog}>
    {leaderDeviceId === null ? null : <GroupPanel leaderDeviceId={leaderDeviceId} titleId={titleId} onClose={onClose} />}
  </ModalDialog>;
}

/** 悬停提示：给不是按钮的图标与控件（状态图标、禁用的滑块）用。 */
function Hint({ text, className, children }: { text: string; className?: string | undefined; children: ReactNode }) {
  const tip = useTooltip(text);
  return <span className={className} {...tip.triggerProps}>{children}{tip.tooltip}</span>;
}

/**
 * 上部是组内设备（主设备在前）：各自的状态、音量与同步微调，成员可以移出；下部是可以加入的设备：
 * 同为 SlimProto、在线、不在任何组里的可见设备。别人在用的设备先确认接管。
 */
function GroupPanel({ leaderDeviceId, titleId, onClose }: { leaderDeviceId: string; titleId: string; onClose: () => void }) {
  const { engine, snapshot } = usePlayer();
  const [error, setError] = useState<unknown>(null);
  const [pending, setPending] = useState<string | null>(null);
  const [takeover, setTakeover] = useState<{ row: PlaybackDeviceRow; who: string } | null>(null);
  const [takeoverPending, setTakeoverPending] = useState(false);
  const leader = snapshot.devices.find((row) => row.device.deviceId === leaderDeviceId);
  const zone = leader?.zone ?? null;
  const members = zone?.group?.members ?? [];
  const memberIds = new Set(members.map((member) => member.deviceId));
  const candidates = snapshot.devices.filter(({ device }) => device.kind === "slimproto" && device.online && device.group === null && device.lan?.unavailableReason == null
    && device.deviceId !== leaderDeviceId && !memberIds.has(device.deviceId));
  const full = members.length + 1 >= MAX_GROUP_DEVICES;
  const rowOf = (deviceId: string) => snapshot.devices.find((row) => row.device.deviceId === deviceId);

  function join(row: PlaybackDeviceRow) {
    if (zone === null) return;
    setError(null);
    if (row.occupant !== null) {
      setTakeover({ row, who: `${row.occupant} 正在使用这台设备` });
      return;
    }
    setPending(row.device.deviceId);
    engine.addGroupMember(zone.zoneId, row.device.deviceId, false).catch((failure: unknown) => {
      // 列表刷新前别人刚开始用：改为确认接管。
      const busy = failureOf(failure);
      if (isHttpFailure(busy, 409, "device_busy")) setTakeover({ row, who: busy?.kind === "http" ? busy.message : "其他成员正在使用这台设备" });
      else setError(failure);
    }).finally(() => setPending(null));
  }
  function confirmTakeover() {
    if (takeover === null || zone === null) return;
    setTakeoverPending(true);
    engine.addGroupMember(zone.zoneId, takeover.row.device.deviceId, true).catch(setError).finally(() => {
      setTakeover(null);
      setTakeoverPending(false);
    });
  }
  function remove(deviceId: string) {
    if (zone === null) return;
    setError(null);
    setPending(deviceId);
    engine.removeGroupMember(zone.zoneId, deviceId).catch(setError).finally(() => setPending(null));
  }

  return <div className={styles.panel}>
    <div className={styles.header}>
      <h2 id={titleId} className={styles.title}>编组{leader === undefined ? "" : `：${leader.device.name}`}</h2>
      <Button variant="icon" icon={<X size={16} />} label="关闭" onClick={onClose} />
    </div>
    {error === null ? null : <FailureNotice error={error} messages={failureMessages} />}
    {leader === undefined ? <p className={styles.note}>这台设备已不可用。</p> : <>
      <section className={styles.section} aria-labelledby={`${titleId}-members`}>
        <h3 id={`${titleId}-members`} className={styles.sectionTitle}>组内设备</h3>
        <ul role="list" className={styles.list}>
          <MemberRow device={leader.device} leader />
          {members.map((member) => {
            const row = rowOf(member.deviceId);
            return <MemberRow key={member.deviceId} device={row?.device ?? null} state={member.state} pending={pending === member.deviceId} onRemove={() => remove(member.deviceId)} />;
          })}
        </ul>
      </section>
      <section className={styles.section} aria-labelledby={`${titleId}-candidates`}>
        <h3 id={`${titleId}-candidates`} className={styles.sectionTitle}>可以加入</h3>
        {candidates.length === 0 ? <p className={styles.note}>没有其他在线的 SlimProto 设备。</p>
          : full ? <p className={styles.note}>一组最多 {MAX_GROUP_DEVICES} 台设备。</p>
          : <ul role="list" className={styles.list}>
            {candidates.map((row) => <li key={row.device.deviceId} className={styles.candidate}>
              <span className={styles.candidateName}>
                <span className={styles.nameText}>{row.device.name}</span>
                {row.occupant === null ? null : <span className={styles.occupant}>{row.occupant} 正在使用</span>}
              </span>
              <Button variant="icon" icon={<Plus size={16} />} label={`加入“${row.device.name}”`} tooltip="加入" busy={pending === row.device.deviceId} onClick={() => join(row)} />
            </li>)}
          </ul>}
      </section>
    </>}
    <p className={styles.footnote}>毫秒级同步，适合不同房间；同一房间的左右声道请用一台播放器。</p>
    <ConfirmDialog
      open={takeover !== null}
      title={`接管“${takeover?.row.device.name ?? ""}”？`}
      confirmLabel="接管"
      pending={takeoverPending}
      onCancel={() => setTakeover(null)}
      onConfirm={confirmTakeover}
    ><p>{takeover?.who}。接管后对方的播放会暂停。</p></ConfirmDialog>
  </div>;
}

/** 组内的一台：主设备标皇冠，成员标状态；看不到的成员（例如被收回授权）只剩移出按钮。 */
function MemberRow({ device, leader = false, state, pending = false, onRemove }: { device: Device | null; leader?: boolean; state?: GroupMemberState; pending?: boolean; onRemove?: () => void }) {
  const { engine } = usePlayer();
  const name = device?.name ?? "看不到的设备";
  const StateIcon = state === undefined ? null : stateIcons[state];
  const stateLabel = state === undefined ? null : groupMemberStateLabels[state];
  return <li className={styles.member}>
    <div className={styles.memberHead}>
      {leader
        ? <Hint text="主设备" className={styles.stateIcon}><Crown size={14} aria-hidden="true" /><span className={styles.visuallyHidden}>主设备：</span></Hint>
        : StateIcon === null || stateLabel === null ? null
        : <Hint text={stateLabel} className={styles.stateIcon} ><StateIcon size={14} aria-hidden="true" data-state={state} /><span className={styles.visuallyHidden}>{stateLabel}：</span></Hint>}
      <span className={styles.nameText}>{name}</span>
      {onRemove === undefined ? null : <Button variant="icon" icon={<Unlink size={16} />} label={`把“${name}”移出组`} tooltip="移出组" busy={pending} onClick={onRemove} />}
    </div>
    {device === null ? null : <div className={styles.controls}>
      <VolumeSlider device={device} volume={engine.volumeOf(device)} onChange={(volume) => engine.setDeviceVolume(device.deviceId, volume)} />
      <SyncDelayField device={device} />
    </div>}
  </li>;
}

function VolumeSlider({ device, volume, onChange }: { device: Device; volume: { volume: number; muted: boolean }; onChange: (volume: number) => void }) {
  const percent = `${Math.round(volume.volume * 100)}%`;
  const Icon = volume.muted || volume.volume === 0 ? VolumeX : Volume1;
  const slider = <input type="range" className={styles.slider} aria-label={`“${device.name}”的音量`} aria-valuetext={device.volumeControllable ? (volume.muted ? `已静音，${percent}` : percent) : "固定音量"}
    min={0} max={1} step={0.01} value={volume.volume} disabled={!device.volumeControllable} data-muted={volume.muted ? "" : undefined} onChange={(event) => onChange(Number(event.target.value))} />;
  return <Hint text={device.volumeControllable ? percent : "固定音量"} className={styles.volume}>
    <Icon size={14} aria-hidden="true" className={styles.controlIcon} />
    {slider}
  </Hint>;
}

/** 同步微调：输入毫秒数，离开输入框或回车时保存；超出范围的值不发出。 */
function SyncDelayField({ device }: { device: Device }) {
  const { engine } = usePlayer();
  const id = useId();
  const saved = device.syncDelayMs ?? 0;
  const [draft, setDraft] = useState<string | null>(null);
  const [invalid, setInvalid] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  function commit() {
    if (draft === null || busy) return;
    const value = Number(draft.trim());
    if (draft.trim() === "" || !Number.isInteger(value) || Math.abs(value) > SYNC_DELAY_LIMIT) {
      setInvalid(true);
      return;
    }
    setInvalid(false);
    if (value === saved) {
      setDraft(null);
      return;
    }
    setBusy(true);
    setError(null);
    engine.setSyncDelay(device.deviceId, value).then(() => setDraft(null), setError).finally(() => setBusy(false));
  }
  return <div className={styles.delay}>
    <Hint text="同步微调：补偿功放或音箱自身的延迟" className={styles.delayInput}>
      <Timer size={14} aria-hidden="true" className={styles.controlIcon} />
      <input
        type="number"
        inputMode="numeric"
        className={styles.number}
        aria-label={`“${device.name}”的同步微调（毫秒）`}
        aria-describedby={`${id}-hint`}
        aria-invalid={invalid ? true : undefined}
        aria-busy={busy ? true : undefined}
        min={-SYNC_DELAY_LIMIT}
        max={SYNC_DELAY_LIMIT}
        step={1}
        value={draft ?? String(saved)}
        onChange={(event) => {
          setDraft(event.target.value);
          setInvalid(false);
        }}
        onBlur={commit}
        onKeyDown={(event) => {
          if (event.key === "Enter") commit();
          if (event.key === "Escape" && draft !== null) {
            // 撤回未保存的输入；Esc 不再关闭整个面板。
            event.preventDefault();
            setDraft(null);
            setInvalid(false);
          }
        }}
      />
      <span className={styles.unit} aria-hidden="true">ms</span>
    </Hint>
    <span id={`${id}-hint`} className={styles.visuallyHidden}>补偿功放或音箱自身的延迟，取值 −500 到 500 毫秒。</span>
    {invalid ? <p className={styles.fieldError}>取值 −500 到 500 毫秒。</p> : null}
    {error === null ? null : <FailureNotice error={error} />}
  </div>;
}
