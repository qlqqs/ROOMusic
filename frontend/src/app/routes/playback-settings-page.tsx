import { PlaybackSettingsForm } from "../../features/player";
import { Page } from "../../shared/ui";

/** 设置 › 播放：音量均衡与交叉渐变。所有登录用户都能用，设置跟随设备。 */
export function PlaybackSettingsPage() {
  return (
    <Page title="播放">
      <PlaybackSettingsForm />
    </Page>
  );
}
