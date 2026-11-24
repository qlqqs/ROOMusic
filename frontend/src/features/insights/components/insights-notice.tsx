import { Link } from "@tanstack/react-router";
import { useViewerScope } from "../../auth";
import { Button, FailureNotice, Notice } from "../../../shared/ui";
import { useAcknowledgeInsightsNotice, useInsightsStatus, useSetInsightsEnabled } from "../hooks/use-insights";
import styles from "./insights.module.css";

/**
 * 概览顶部的说明（管理员）：匿名统计默认开启，管理员确认前一直显示；“知道了”与“关闭统计”都记为已确认（存在服务端，换浏览器也不再出现）。
 * 统计没开（部署配置关闭或已在设置里关掉）时不显示。
 */
export function InsightsNotice() {
  const { viewer } = useViewerScope();
  return viewer.role === "admin" ? <AdminInsightsNotice /> : null;
}

function AdminInsightsNotice() {
  const status = useInsightsStatus();
  const acknowledge = useAcknowledgeInsightsNotice();
  const disable = useSetInsightsEnabled();
  const data = status.data;
  if (data === undefined || data.notice_acknowledged || data.forced_off || !data.enabled) return null;
  const pending = acknowledge.isPending || disable.isPending;
  const error = acknowledge.error ?? disable.error;
  return (
    <Notice
      tone="info"
      title="匿名统计"
      action={
        <span className={styles.actions}>
          <Link to="/settings/insights">查看发送内容</Link>
          <Button disabled={pending} onClick={() => disable.mutate(false, { onSuccess: () => acknowledge.mutate() })}>
            关闭统计
          </Button>
          <Button variant="primary" disabled={pending} onClick={() => acknowledge.mutate()}>
            知道了
          </Button>
        </span>
      }
    >
      <p className={styles.noticeText}>
        ROOMusic 每天向项目发送一次匿名统计：版本、曲库规模的分档、用到了哪些功能。不含曲目、文件名、账号或 IP，可以随时在设置里关闭。
      </p>
      {error === null ? null : <FailureNotice error={error} context="write" title="没有保存" />}
    </Notice>
  );
}
