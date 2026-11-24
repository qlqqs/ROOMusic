import { AboutSettings } from "../../features/insights";
import { Page } from "../../shared/ui";

/** 设置 › 关于：许可与源代码地址（所有人），管理员另外看到版本。 */
export function AboutPage() {
  return (
    <Page title="关于">
      <AboutSettings />
    </Page>
  );
}
