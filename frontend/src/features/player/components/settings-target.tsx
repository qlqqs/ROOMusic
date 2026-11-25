import { createContext, useContext, type ReactNode } from "react";
import type { PlaybackSettings } from "../zones/model";
import { defaultSettings } from "../zones/controller";
import { usePlayerEngine, usePlayerSelector } from "../player-provider";

/**
 * 设置页正在编辑的播放区：默认是正在控制的那个；选了第三方客户端的设备时是它的播放区。
 * client 为 true 时只给客户端用得上的设置（无缝播放、交叉渐变由客户端自己负责）。
 */
export type SettingsTarget = { settings: PlaybackSettings; disabled: boolean; client: boolean; set: (patch: Partial<PlaybackSettings>) => void };

const TargetContext = createContext<string | null>(null);

/** 让里面的设置表单、均衡编辑与 IR 管理都作用于这个播放区；zoneId 为 null 时作用于正在控制的播放区。 */
export function SettingsTargetProvider({ zoneId, children }: { zoneId: string | null; children: ReactNode }) {
  return <TargetContext value={zoneId}>{children}</TargetContext>;
}

export function useSettingsTarget(): SettingsTarget {
  const zoneId = useContext(TargetContext);
  const engine = usePlayerEngine();
  const controlled = usePlayerSelector((snapshot) => snapshot.settings);
  const controlledReady = usePlayerSelector((snapshot) => snapshot.zone !== null);
  const client = usePlayerSelector((snapshot) => (zoneId === null ? undefined : snapshot.clients.find((row) => row.device.zoneId === zoneId)?.zone ?? null));
  if (zoneId === null) return { settings: controlled, disabled: !controlledReady, client: false, set: (patch) => engine.setPlaybackSettings(patch) };
  return { settings: client?.settings ?? defaultSettings, disabled: client == null, client: true, set: (patch) => engine.setZoneSettings(zoneId, patch) };
}
