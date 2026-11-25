import { Check, Copy, KeyRound } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Button, FailureNotice, SettingRow, SettingsGroup, StatusTag } from "../../../shared/ui";
import { useSubsonicConnection } from "../hooks/use-subsonic";
import { ConfirmPasswordDialog } from "./confirm-password-dialog";
import { JukeboxZoneRow, type JukeboxZoneOption } from "./jukebox-zone";
import styles from "./subsonic.module.css";

/** 客户端里填的服务器地址：当前页面的来源，客户端自己在后面拼 /rest/…。 */
export function serverAddress(origin: string): string {
  return `${origin}/`;
}

/**
 * 设置 › 账号里的“第三方客户端”一组：服务器地址、用户名（各带复制）与登录密码的状态。
 * 实例关闭 Subsonic 接口时整组不显示；加载中不占位，读取失败给一句话。
 * zones 是点唱机可以控制的本人播放区，由页面从播放设备里算出（subsonic 不导入 player）；还没读到时为 null。
 */
export function ClientConnectionSettings({ zones }: { zones: readonly JukeboxZoneOption[] | null }) {
  const connection = useSubsonicConnection();
  const headingRef = useRef<HTMLHeadingElement>(null);
  const [dialogOpen, setDialogOpen] = useState(false);
  if (connection.isError) {
    return (
      <SettingsGroup title="第三方客户端">
        <FailureNotice error={connection.error} context="read" title="没有读到连接方式" />
      </SettingsGroup>
    );
  }
  const data = connection.data;
  if (data === undefined || !data.enabled) return null;
  return (
    <SettingsGroup title="第三方客户端" headingRef={headingRef}>
      <SettingRow label="服务器地址" description="在 Symfonium、Feishin 等支持 Subsonic 的客户端里填写。" plainLabel>
        <CopyValue name="服务器地址" value={serverAddress(window.location.origin)} />
      </SettingRow>
      <SettingRow label="用户名" plainLabel>
        <CopyValue name="用户名" value={data.username} />
      </SettingRow>
      <SettingRow
        label="密码"
        description={data.ready ? "用登录密码连接。" : "用登录密码连接。大多数客户端用令牌方式登录，需要先在这里确认一次密码。"}
        plainLabel
      >
        {data.ready ? (
          <StatusTag icon={<Check size={14} />}>已就绪</StatusTag>
        ) : (
          <Button icon={<KeyRound size={16} />} onClick={() => setDialogOpen(true)}>
            确认密码
          </Button>
        )}
      </SettingRow>
      <JukeboxZoneRow saved={data.jukebox_zone_id} zones={zones} />
      <ConfirmPasswordDialog
        open={dialogOpen}
        username={data.username}
        onClose={(confirmed) => {
          setDialogOpen(false);
          // 确认成功后打开对话框的按钮已换成“已就绪”，焦点回到这一组的标题。
          if (confirmed) headingRef.current?.focus();
        }}
      />
    </SettingsGroup>
  );
}

/**
 * 只读的值加一个复制图标。值本身可以选中，浏览器不提供剪贴板接口时（经 http 访问局域网里的实例）不显示复制按钮，手动复制。
 * 复制后图标换成对勾、读屏名换成“已复制”，两秒后复原。
 */
function CopyValue({ name, value }: { name: string; value: string }) {
  const [copied, setCopied] = useState(false);
  const canCopy = typeof navigator !== "undefined" && navigator.clipboard !== undefined;
  useEffect(() => {
    if (!copied) return;
    const timer = window.setTimeout(() => setCopied(false), 2000);
    return () => window.clearTimeout(timer);
  }, [copied]);
  return (
    <span className={styles.copyValue}>
      <span className={styles.value}>{value}</span>
      {canCopy ? (
        <Button
          variant="icon"
          icon={copied ? <Check size={16} /> : <Copy size={16} />}
          label={copied ? `已复制${name}` : `复制${name}`}
          onClick={() => {
            void navigator.clipboard.writeText(value).then(() => setCopied(true));
          }}
        />
      ) : null}
    </span>
  );
}
