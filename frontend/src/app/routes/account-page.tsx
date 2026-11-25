import { ChangePasswordButton, roleLabels, useViewerScope } from "../../features/auth";
import { ClientConnectionSettings } from "../../features/subsonic";
import { Page, SettingRow, SettingsGroup } from "../../shared/ui";
import styles from "./account-page.module.css";

/** 设置 › 账号：当前账号的用户名、角色与修改密码一张卡片；实例开启 Subsonic 接口时，下面再有“第三方客户端”一组。所有登录用户都能用。 */
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
      <ClientConnectionSettings />
    </Page>
  );
}
