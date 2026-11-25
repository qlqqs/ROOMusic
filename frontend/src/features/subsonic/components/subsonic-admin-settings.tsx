import { useState } from "react";
import { ConfirmDialog, FailureNotice, QueryContent, SettingRow, SettingsGroup, Skeleton, Switch } from "../../../shared/ui";
import { useSetSubsonicEnabled, useSubsonicSettings } from "../hooks/use-subsonic";

/**
 * 设置 › 第三方客户端（管理员）：Subsonic 兼容接口的实例开关。打开直接生效；关闭会删掉全部登录密码副本，先确认一次。
 */
export function SubsonicAdminSettings() {
  const settings = useSubsonicSettings();
  const update = useSetSubsonicEnabled();
  const [confirmOff, setConfirmOff] = useState(false);
  return (
    <QueryContent query={settings} loading={<Skeleton label="正在加载第三方客户端设置" lines={2} />}>
      {(data) => {
        const enabled = update.isPending ? update.variables : data.enabled;
        return (
          <>
            <SettingsGroup
              title="Subsonic 兼容接口"
              footer={update.isError && !confirmOff ? <FailureNotice error={update.error} context="write" title="开关没有改成" /> : undefined}
            >
              <SettingRow
                label="允许第三方客户端连接"
                description="Symfonium、Feishin、Amperfy 等客户端可以用成员的用户名和登录密码连接。为了支持客户端常用的令牌登录，服务器会用实例密钥加密保存每位成员的一份登录密码；开启前就有的成员，要在网页登录一次或在账号页确认一次密码。"
              >
                {({ controlId, descriptionId }) => (
                  <Switch
                    id={controlId}
                    aria-describedby={descriptionId}
                    checked={enabled}
                    disabled={update.isPending}
                    onChange={(next) => {
                      update.reset();
                      if (next) update.mutate(true);
                      else setConfirmOff(true);
                    }}
                  />
                )}
              </SettingRow>
            </SettingsGroup>
            <ConfirmDialog
              open={confirmOff}
              title="关闭第三方客户端连接？"
              confirmLabel="关闭"
              pending={update.isPending}
              error={update.isError ? <FailureNotice error={update.error} context="write" title="没有关闭" /> : undefined}
              onConfirm={() => update.mutate(false, { onSuccess: () => setConfirmOff(false) })}
              onCancel={() => {
                update.reset();
                setConfirmOff(false);
              }}
            >
              已连接的客户端会立即无法使用，保存的登录密码副本会全部删除。以后再开启，成员需要重新登录网页或在账号页确认一次密码。
            </ConfirmDialog>
          </>
        );
      }}
    </QueryContent>
  );
}
