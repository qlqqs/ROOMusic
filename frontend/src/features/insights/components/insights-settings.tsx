import { FailureNotice, QueryContent, SettingRow, SettingsGroup, Skeleton, Switch } from "../../../shared/ui";
import { formatDateTime } from "../../../shared/lib/time";
import type { InsightsStatusDTO } from "../api/decoders";
import { useInsightsStatus, useSetInsightsEnabled } from "../hooks/use-insights";
import styles from "./insights.module.css";

const switchFailures = { insights_forced_off: "部署配置（ROOMUSIC_INSIGHTS_DISABLED）已关闭匿名统计，这里改不了。" } as const;

/** 上次发送的结果：错误码换成一句话。 */
function lastResult(status: InsightsStatusDTO): string {
  if (status.last_attempt_at === null) return `还没有发送过；最早在 ${formatDateTime(status.first_send_after)} 之后发送第一次。`;
  const when = formatDateTime(status.last_attempt_at);
  if (status.last_error === null) return `${when} 发送成功。`;
  if (status.last_error === "network") return `${when} 没有连上统计端，明天再试。`;
  if (status.last_error === "invalid_response") return `${when} 统计端的回应不对，明天再试。`;
  const code = /^http_(\d{3})$/.exec(status.last_error)?.[1];
  return code === undefined ? `${when} 发送失败，明天再试。` : `${when} 统计端返回 ${code}，明天再试。`;
}

/** 设置 › 匿名统计（管理员）：开关、上次发送、将要发送的完整内容。 */
export function InsightsSettings() {
  const status = useInsightsStatus();
  const update = useSetInsightsEnabled();
  return (
    <QueryContent query={status} loading={<Skeleton label="正在加载匿名统计" lines={3} />}>
      {(data) => {
        const enabled = update.isPending ? update.variables : data.enabled;
        return (
          <>
            <SettingsGroup title="匿名统计" footer={update.isError ? <FailureNotice error={update.error} context="write" title="开关没有改成" messages={switchFailures} /> : undefined}>
              <SettingRow
                label="发送匿名统计"
                description={
                  data.forced_off
                    ? "部署配置（ROOMUSIC_INSIGHTS_DISABLED）已关闭，不会发送。"
                    : `每天向 ${data.endpoint_host} 发送一次，帮助项目了解有多少实例在用、用到了哪些功能。不含曲目、文件名、账号、地址或 IP。`
                }
              >
                {({ controlId, descriptionId }) => (
                  <Switch id={controlId} aria-describedby={descriptionId} checked={!data.forced_off && enabled} disabled={data.forced_off || update.isPending} onChange={(next) => update.mutate(next)} />
                )}
              </SettingRow>
              {data.forced_off || !data.enabled ? null : (
                <SettingRow label="上次发送" plainLabel>
                  <span className={styles.value}>{lastResult(data)}</span>
                </SettingRow>
              )}
              <SettingRow label="公开的统计汇总" plainLabel>
                <a href={data.public_page_url} target="_blank" rel="noreferrer">
                  {data.public_page_url.replace(/^https?:\/\//, "").replace(/\/$/, "")}
                </a>
              </SettingRow>
            </SettingsGroup>
            <SettingsGroup title="发送的内容">
              <p className={styles.explain}>
                下面就是将要发送的完整内容，和实际发出去的相同。实例 ID 是首次启动时随机生成的；数字都是分档，例如曲目数只发“1k-10k”这样的范围。
              </p>
              <pre className={styles.payload} aria-label="将要发送的内容">{JSON.stringify(data.payload, null, 2)}</pre>
            </SettingsGroup>
          </>
        );
      }}
    </QueryContent>
  );
}
