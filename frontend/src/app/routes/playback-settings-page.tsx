import { PlaybackSettingsDevice, PlaybackSettingsForm } from "../../features/player";
import { Page } from "../../shared/ui";

/** 设置 › 播放：音量均衡、无缝、交叉渐变与音质。改的是正在控制的那台设备，标题旁写着它的名字。 */
export function PlaybackSettingsPage() {
  return (
    <Page title="播放" titleAside={<PlaybackSettingsDevice />}>
      <PlaybackSettingsForm />
    </Page>
  );
}
