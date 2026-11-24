import { LanDevicesSettings } from "../../features/lan-devices";
import { Page } from "../../shared/ui";

/** 设置 › 局域网设备：查找与使用局域网里的 UPnP/DLNA 播放设备、账号间共享与分配、手动添加。管理员专用，由路由层的 AdminGate 把关。 */
export function LanDevicesPage() {
  return (
    <Page title="局域网设备" description="服务器直接把播放推给家里的功放、音箱或电视；音效处理照常生效。">
      <LanDevicesSettings />
    </Page>
  );
}
