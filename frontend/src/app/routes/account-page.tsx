import { ChangePasswordButton, roleLabels, useViewerScope } from "../../features/auth";
import { Page, SettingRow, SettingsGroup } from "../../shared/ui";
import styles from "./account-page.module.css";

/** 设置 › 账号：当前账号的用户名、角色与修改密码，一张卡片三行。所有登录用户都能用。 */
export function AccountPage() {
  const { viewer } = useViewerScope();
  return (
    <Page title="账号">
      <SettingsGroup>
        <SettingRow label="用户名" plainLabel>
          <span className={styles.value}>{viewer.username}</span>
        </SettingRow>
        <SettingRow label="角色" plainLabel>
          <span className={styles.value}>{roleLabels[viewer.role]}</span>
        </SettingRow>
        <SettingRow label="密码" plainLabel>
          <ChangePasswordButton />
        </SettingRow>
      </SettingsGroup>
    </Page>
  );
}
