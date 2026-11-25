import { AboutSettings } from "../../features/insights";
import { Page } from "../../shared/ui";
import logoUrl from "../brand/logo-small.svg";

/** 设置 › 关于：标志、名称与项目链接（所有人），管理员另外看到版本并可复制诊断信息。 */
export function AboutPage() {
  return (
    <Page title="关于">
      <AboutSettings mark={<img src={logoUrl} alt="" />} />
    </Page>
  );
}
