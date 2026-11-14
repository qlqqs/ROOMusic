import { PlaybackSettingsDevice, PlaybackSettingsForm } from "../../features/player";
import { Page } from "../../shared/ui";

/** 设置 › 播放：分组的播放、输出与音效处理设置。改的是正在控制的那台设备，页首写着它的名字。 */
export function PlaybackSettingsPage() {
  return (
    <Page title="播放" description={<PlaybackSettingsDevice />}>
      <PlaybackSettingsForm />
    </Page>
  );
}
