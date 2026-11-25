import { useNavigate } from "@tanstack/react-router";
import { PlaybackSettingsDevice, PlaybackSettingsForm, PlaybackSettingsTarget, useClientDevice } from "../../features/player";
import { Page } from "../../shared/ui";

/**
 * 设置 › 播放：分组的播放、输出与音效处理设置。默认改正在控制的那台设备，页首写着它的名字；
 * 有第三方客户端登记的设备时，顶部可以改选其中一台（地址里的 device），表单随之改它的播放区。
 */
export function PlaybackSettingsPage({ deviceId }: { deviceId: string | null }) {
  const navigate = useNavigate();
  const client = useClientDevice(deviceId);
  return (
    <Page title="播放" description={<PlaybackSettingsDevice deviceId={deviceId} />}>
      <PlaybackSettingsTarget deviceId={deviceId} onSelect={(next) => void navigate({ to: "/settings/playback", search: next === null ? {} : { device: next }, replace: true })} />
      <PlaybackSettingsForm key={client?.device.zoneId ?? "controlled"} zoneId={client?.device.zoneId ?? null} />
    </Page>
  );
}
