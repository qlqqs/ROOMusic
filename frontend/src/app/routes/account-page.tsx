import { useMemo } from "react";
import { ChangePasswordButton, roleLabels, useViewerScope } from "../../features/auth";
import { usePlayerSelector, type PlaybackDeviceRow } from "../../features/player";
import { ClientConnectionSettings, type JukeboxZoneOption } from "../../features/subsonic";
import { Page, SettingRow, SettingsGroup } from "../../shared/ui";
import styles from "./account-page.module.css";

/** 点唱机能控制的播放区：每台设备一个；作为成员加入了别人编组的设备不单列，编组按主设备列出并带台数。 */
export function jukeboxZoneOptions(rows: readonly PlaybackDeviceRow[]): JukeboxZoneOption[] {
  return rows
    .filter((row) => row.device.group?.role !== "member")
    .map(({ device, zone }) => {
      const members = zone?.group?.members.length ?? 0;
      return { id: device.zoneId, label: members > 0 ? `${device.name}（编组 ${members + 1} 台）` : device.name };
    });
}

/** 设置 › 账号：当前账号的用户名、角色与修改密码一张卡片；实例开启 Subsonic 接口时，下面再有“第三方客户端”一组。所有登录用户都能用。 */
export function AccountPage() {
  const { viewer } = useViewerScope();
  const rows = usePlayerSelector((snapshot) => snapshot.devices);
  const restoring = usePlayerSelector((snapshot) => snapshot.restoring);
  const zones = useMemo(() => (restoring ? null : jukeboxZoneOptions(rows)), [restoring, rows]);
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
      <ClientConnectionSettings zones={zones} />
    </Page>
  );
}
