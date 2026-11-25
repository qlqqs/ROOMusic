import { Trash2 } from "lucide-react";
import { useState } from "react";
import { Button, ConfirmDialog, FailureNotice, SettingRow, SettingsGroup } from "../../../shared/ui";
import { formatRelativeTime } from "../../../shared/lib/time";
import { usePlayerEngine, usePlayerSelector } from "../player-provider";
import type { ClientDeviceRow } from "../zones/controller";
import styles from "./playback-settings-form.module.css";

const CONTROLLED = "";

/** 地址里的设备 ID 对应的第三方客户端设备；没有（未选、已删除、不是客户端）时为 null。 */
export function useClientDevice(deviceId: string | null): ClientDeviceRow | null {
  return usePlayerSelector((snapshot) => (deviceId === null ? null : snapshot.clients.find((row) => row.device.deviceId === deviceId) ?? null));
}

/**
 * 设置页顶部的“设置的设备”：默认改正在控制的设备，也可以改某个第三方客户端的设备。没有客户端设备时不渲染。
 * 选了客户端时行尾有删除：它的设置一并删除，客户端下次连接时重新登记、恢复默认设置。
 */
export function PlaybackSettingsTarget({ deviceId, onSelect }: { deviceId: string | null; onSelect: (deviceId: string | null) => void }) {
  const engine = usePlayerEngine();
  const clients = usePlayerSelector((snapshot) => snapshot.clients);
  const controlledName = usePlayerSelector((snapshot) => snapshot.device?.name ?? null);
  const selected = useClientDevice(deviceId);
  const [deleting, setDeleting] = useState<ClientDeviceRow | null>(null);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<unknown>(null);
  if (clients.length === 0) return null;
  const confirmDelete = () => {
    if (deleting === null) return;
    setPending(true);
    engine.deleteDevice(deleting.device.deviceId).then(
      () => {
        setPending(false);
        setDeleting(null);
        onSelect(null);
      },
      (failure: unknown) => {
        setPending(false);
        setError(failure);
      },
    );
  };
  return (
    <SettingsGroup>
      <SettingRow label="设置的设备" description="第三方客户端在自己那里播放，这里的设置作用于它从 ROOMusic 取的音频。">
        {(ids) => (
          <span className={styles.target}>
            <select id={ids.controlId} aria-describedby={ids.descriptionId} className={styles.select} value={selected?.device.deviceId ?? CONTROLLED} onChange={(event) => onSelect(event.target.value === CONTROLLED ? null : event.target.value)}>
              <option value={CONTROLLED}>{controlledName === null ? "正在控制的设备" : `正在控制：${controlledName}`}</option>
              {clients.map((row) => (
                <option key={row.device.deviceId} value={row.device.deviceId}>
                  {`${row.device.name}（第三方客户端）`}
                </option>
              ))}
            </select>
            {selected === null ? null : (
              <Button variant="icon" icon={<Trash2 size={16} />} label={`删除“${selected.device.name}”`} onClick={() => { setError(null); setDeleting(selected); }} />
            )}
          </span>
        )}
      </SettingRow>
      <ConfirmDialog
        open={deleting !== null}
        title={`删除“${deleting?.device.name ?? ""}”？`}
        confirmLabel="删除"
        pending={pending}
        error={error === null ? undefined : <FailureNotice error={error} context="write" />}
        onCancel={() => setDeleting(null)}
        onConfirm={confirmDelete}
      >
        <p>它的播放设置会一并删除。这个客户端下次连接时会重新登记，设置恢复为默认（不做任何处理，直接发原文件）。</p>
      </ConfirmDialog>
    </SettingsGroup>
  );
}

/** 页首的一句话：设置作用于哪台设备；客户端设备再写最近一次使用的时间。还没有设备时不渲染。 */
export function PlaybackSettingsDevice({ deviceId = null }: { deviceId?: string | null }) {
  const name = usePlayerSelector((snapshot) => snapshot.device?.name ?? null);
  const client = useClientDevice(deviceId);
  if (client !== null) {
    return <>这些设置作用于第三方客户端：<span className={styles.deviceName}>{client.device.name}</span>。最近使用于{formatRelativeTime(new Date(client.device.lastSeenAt).toISOString())}。</>;
  }
  if (name === null) return null;
  return <>这些设置作用于正在控制的设备：<span className={styles.deviceName}>{name}</span></>;
}
