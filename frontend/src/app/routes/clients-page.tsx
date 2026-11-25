import { SubsonicAdminSettings } from "../../features/subsonic";
import { Page } from "../../shared/ui";

/** 设置 › 第三方客户端：Subsonic 兼容接口的实例开关。管理员专用，由路由层的 AdminGate 把关。 */
export function ClientsPage() {
  return (
    <Page title="第三方客户端" description="让支持 Subsonic 的手机和桌面客户端连接这台服务器。">
      <SubsonicAdminSettings />
    </Page>
  );
}
