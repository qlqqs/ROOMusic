import { ChangePasswordButton, roleLabels, useViewerScope } from "../../features/auth";
import { Page } from "../../shared/ui";
import styles from "./account-page.module.css";

/** 设置 › 账号：当前账号的用户名、角色与修改密码。所有登录用户都能用。 */
export function AccountPage() {
  const { viewer } = useViewerScope();
  return (
    <Page title="账号">
      <dl className={styles.details}>
        <div className={styles.item}>
          <dt className={styles.label}>用户名</dt>
          <dd className={styles.value}>{viewer.username}</dd>
        </div>
        <div className={styles.item}>
          <dt className={styles.label}>角色</dt>
          <dd className={styles.value}>{roleLabels[viewer.role]}</dd>
        </div>
      </dl>
      <div>
        <ChangePasswordButton />
      </div>
    </Page>
  );
}
