import { useState } from "react";
import { formatDateTime } from "../../../shared/lib/time";
import { ConfirmDialog, FailureNotice, QueryContent, SettingRow, SettingsGroup, Skeleton, Switch } from "../../../shared/ui";
import type { CurationSettingsDTO, CurationSourceDTO } from "../api/decoders";
import { sentToSourceText } from "../curation-text";
import { useCurationSettings, useCurationSources, useUpdateCurationSettings } from "../hooks/use-curation";
import styles from "./curation-sections.module.css";

/** 总开关那一行的说明：开着时写谁在什么时候打开；打开它的管理员不可用时说明后台已停止；关着时说明打开后会做什么。 */
function switchDescription(settings: CurationSettingsDTO): string {
  if (!settings.enabled) return "关闭时不向外部服务发任何请求。打开之后后台自动补全，已写入的内容随时可以撤销。";
  if (settings.inactive_reason === "enabler_unavailable") return "打开它的管理员已不可用，后台已经停止。";
  const who = settings.enabled_by === null ? "" : `由 ${settings.enabled_by.username} `;
  const when = settings.enabled_at === null ? "" : `于 ${formatDateTime(settings.enabled_at)} `;
  return `${who}${when}打开。关闭后不再向外部发请求，已写入的内容保留，可以按来源撤销。`;
}

/**
 * “自动整理”卡片：总开关与暂停。打开总开关意味着向外部服务发送查询并使用它们的资料，这一步由人确认（ADR 0006 第 10 节），
 * 所以从关到开先弹确认框，列出当前开着的源会收到什么；关闭与暂停不确认。暂停只在总开关开着时出现（从属项随开关出现）。
 * 保存中显示要改成的值，失败时回到服务器的值并就近提示。
 */
export function CurationSwitchSection() {
  const settings = useCurationSettings();
  const sources = useCurationSources();
  const update = useUpdateCurationSettings();
  const [confirming, setConfirming] = useState(false);

  const pendingEnabled = update.isPending ? update.variables.enabled : undefined;
  const pendingPaused = update.isPending ? update.variables.paused : undefined;

  return (
    <SettingsGroup
      title="自动整理"
      footer={update.isError ? <FailureNotice error={update.error} context="write" title="设置没有改成" /> : null}
    >
      <QueryContent query={settings} loading={<Skeleton label="正在加载自动整理设置" lines={2} />}>
        {(data) => {
          const enabled = pendingEnabled ?? data.enabled;
          return (
            <>
              <SettingRow label="自动整理" description={switchDescription(data)}>
                {({ controlId, descriptionId }) => (
                  <Switch
                    id={controlId}
                    aria-describedby={descriptionId}
                    checked={enabled}
                    disabled={update.isPending}
                    onChange={(checked) => (checked ? setConfirming(true) : update.mutate({ enabled: false }))}
                  />
                )}
              </SettingRow>
              {enabled ? (
                <SettingRow label="暂停" description="暂停后台的匹配与下载，已写入的内容不变。" nested>
                  {({ controlId, descriptionId }) => (
                    <Switch
                      id={controlId}
                      aria-describedby={descriptionId}
                      checked={pendingPaused ?? data.paused}
                      disabled={update.isPending}
                      onChange={(checked) => update.mutate({ paused: checked })}
                    />
                  )}
                </SettingRow>
              ) : null}
            </>
          );
        }}
      </QueryContent>
      <ConfirmDialog
        open={confirming}
        title="打开自动整理？"
        confirmLabel="打开"
        onCancel={() => setConfirming(false)}
        onConfirm={() => {
          setConfirming(false);
          update.mutate({ enabled: true });
        }}
      >
        <EnableConsent sources={sources.data?.items} />
      </ConfirmDialog>
    </SettingsGroup>
  );
}

/** 打开前的说明：这台服务器会直接连接哪些网站、各自收到什么；不发送什么；之后不再逐项询问。 */
function EnableConsent({ sources }: { sources: readonly CurationSourceDTO[] | undefined }) {
  const enabled = (sources ?? []).filter((source) => source.enabled);
  return (
    <div className={styles.consent}>
      <p>打开之后，这台服务器会在后台直接连接下面这些网站，把曲库和它们的资料对上，自动补全封面、歌词与空着的信息：</p>
      {enabled.length === 0 ? (
        <p>现在没有开着的外部源，打开后不会发出请求。</p>
      ) : (
        <ul>
          {enabled.map((source) => (
            <li key={source.id}>
              <strong>{source.name}</strong>
              {source.keys?.some((key) => !key.configured && key.optional !== true) === true ? "（填入密钥后）" : ""}：{sentToSourceText(source)}
            </li>
          ))}
        </ul>
      )}
      <p>不发送音频、文件路径与这台服务器的信息。之后不再逐项询问；每一项自动修改都可以撤销。</p>
    </div>
  );
}
