import { ProxySettings } from "../../features/network";
import { Page } from "../../shared/ui";

/** 设置 › 网络：全局出站代理。管理员专用，由路由层的 AdminGate 把关。 */
export function NetworkPage() {
  return (
    <Page title="网络" description="服务器访问外网的方式；局域网设备与本机地址不受影响。">
      <ProxySettings />
    </Page>
  );
}
