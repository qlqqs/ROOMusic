import { Link, useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { pageCountOf } from "../../../shared/lib/pagination";
import { formatDateTime } from "../../../shared/lib/time";
import { useClampedPage } from "../../../shared/lib/use-clamped-page";
import { Button, ConfirmDialog, DataTable, FailureNotice, Pagination, QueryContent, SettingsGroup, Skeleton, VisuallyHidden, type Column } from "../../../shared/ui";
import type { CurationExceptionDTO } from "../api/decoders";
import { EXCEPTION_MESSAGES, exceptionChangeLines, exceptionExternal, exceptionLinkVerb, exceptionReviewText, riskText } from "../curation-text";
import { isRetryableAcceptFailure, useAcceptException, useCurationExceptions, useDeclineException } from "../hooks/use-curation";
import styles from "./curation-sections.module.css";

type Decision = { exception: CurationExceptionDTO; action: "accept" | "decline" };

/**
 * “例外”卡片（M7 D、E2）：后台审查没通过或转给人确认的提案，新的在前，等管理员采纳或不采纳。
 * 采纳由服务器按当时的状态重新核对后执行，记为当前管理员的人工确认；不采纳写“不再提出”的标记。两者都先确认。
 * 外部来的文字（外部编号、审查理由、前后值）按不可信文本显示。页码来自 URL（exception_page）；mine 时（当前管理员是开启者）给“查看过程”。
 * refresh 让目录与详情在采纳之后更新（由 app 提供）。
 */
export function CurationExceptionsSection({ page, mine, refresh }: { page: number; mine: boolean; refresh: () => void }) {
  const exceptions = useCurationExceptions(page);
  const accept = useAcceptException(refresh);
  const decline = useDeclineException();
  const navigate = useNavigate();
  const [decision, setDecision] = useState<Decision | null>(null);

  const pagination = exceptions.data;
  const outOfRange = useClampedPage({
    page,
    pageCount: pagination === undefined ? 0 : pageCountOf(pagination.total, pagination.page_size),
    ready: pagination !== undefined && !exceptions.isPlaceholderData,
    onOutOfRange: (lastPage) =>
      void navigate({ to: "/settings/curation", search: (previous) => ({ ...previous, exception_page: lastPage > 1 ? lastPage : undefined }), replace: true }),
  });

  const mutation = decision?.action === "decline" ? decline : accept;
  const close = () => {
    setDecision(null);
    accept.reset();
    decline.reset();
  };
  const confirm = () => {
    if (decision === null) return;
    // 失败时框留着写原因：外部源暂时不可用时可以“重试”（同一个人续做已写了一半的匹配），其余错误列表已刷新。
    const options = { onSuccess: () => setDecision(null) };
    if (decision.action === "accept") accept.mutate(decision.exception.id, options);
    else decline.mutate(decision.exception.id, options);
  };

  const columns: Column<CurationExceptionDTO>[] = [
    { id: "created", header: "提出时间", rowHeader: true, cell: (item) => formatDateTime(item.created_at) },
    {
      id: "release",
      header: "发行",
      cell: (item) => (
        <Link to="/settings/operations" search={{ release: item.release.id }}>
          <span className={styles.untrusted}>{releaseName(item)}</span>
          <VisuallyHidden>的操作记录</VisuallyHidden>
        </Link>
      ),
    },
    { id: "change", header: "提出的修改", cell: (item) => <ChangeLines exception={item} /> },
    {
      id: "review",
      header: "审查",
      cell: (item) => (
        <ul className={styles.lines}>
          <li>
            {exceptionReviewText(item)}
            {item.review.risk === null ? "" : `（风险${riskText[item.review.risk]}）`}
          </li>
          {item.review.reasons.map((reason, index) => (
            <li key={index} className={styles.untrusted}>
              {reason}
            </li>
          ))}
        </ul>
      ),
    },
    {
      id: "actions",
      header: "操作",
      hideLabelWhenStacked: true,
      cell: (item) => (
        <span className={styles.actions}>
          <Button variant="primary" onClick={() => setDecision({ exception: item, action: "accept" })}>
            采纳
            <VisuallyHidden>对{releaseName(item)}的修改</VisuallyHidden>
          </Button>
          <Button onClick={() => setDecision({ exception: item, action: "decline" })}>
            不采纳
            <VisuallyHidden>对{releaseName(item)}的修改</VisuallyHidden>
          </Button>
          {mine && item.conversation_id !== null ? (
            <Link to="/steward/$conversationId" params={{ conversationId: item.conversation_id }}>
              查看过程
              <VisuallyHidden>：{releaseName(item)}</VisuallyHidden>
            </Link>
          ) : null}
        </span>
      ),
    },
  ];

  const data = exceptions.data;
  const listed = data !== undefined && !outOfRange && data.total > 0 ? data : null;
  return (
    <SettingsGroup
      title="例外"
      footer={
        listed === null ? null : (
          <Pagination
            label="例外分页"
            page={listed.page}
            pageCount={pageCountOf(listed.total, listed.page_size)}
            renderLink={(targetPage, props) => (
              <Link to="/settings/curation" search={(previous) => ({ ...previous, exception_page: targetPage > 1 ? targetPage : undefined })} {...props} />
            )}
          />
        )
      }
    >
      <p className={styles.groupNote}>模型提出、但审查没通过或转给你确认的修改。采纳记为你的人工确认；不采纳的，以后不再提出。</p>
      <QueryContent query={exceptions} loading={<Skeleton label="正在加载例外" lines={3} />}>
        {(page) =>
          outOfRange ? (
            <Skeleton label="正在加载例外" lines={3} />
          ) : page.total === 0 ? (
            <p>没有等你处理的例外。</p>
          ) : (
            <div aria-busy={exceptions.isPlaceholderData}>
              <DataTable caption="等你处理的例外" columns={columns} rows={page.items} rowKey={(item) => item.id} />
            </div>
          )
        }
      </QueryContent>
      <ConfirmDialog
        open={decision !== null}
        title={decision?.action === "decline" ? "不采纳这项修改？" : "采纳这项修改？"}
        confirmLabel={decision?.action === "decline" ? "不采纳" : mutation.isError && isRetryableAcceptFailure(mutation.error) ? "重试" : "采纳"}
        pending={mutation.isPending}
        error={mutation.isError ? <FailureNotice error={mutation.error} context="write" messages={EXCEPTION_MESSAGES} /> : undefined}
        onCancel={close}
        onConfirm={confirm}
      >
        {decision === null ? null : (
          <div className={styles.consent}>
            <p>
              <span className={styles.untrusted}>{releaseName(decision.exception)}</span>
            </p>
            <ChangeLines exception={decision.exception} />
            <p>
              {decision.action === "accept"
                ? decision.exception.tool === "correct_release"
                  ? "按现在的内容重新核对后写入，记为你的人工确认，字段写进人工修正。"
                  : "按现在的内容重新核对后写入，记为你的人工确认；之后可以在操作记录里撤销。"
                : "以后程序与模型都不再提出这个值；可以在“不再提出”里清除。"}
            </p>
          </div>
        )}
      </ConfirmDialog>
    </SettingsGroup>
  );
}

function releaseName(exception: CurationExceptionDTO): string {
  const { title, artist } = exception.release;
  return artist === "" ? title || "（无标题）" : `${title || "（无标题）"} · ${artist}`;
}

/** 提出的修改，逐行：匹配先写外部对应（带 MusicBrainz 链接），再写其余各项。 */
function ChangeLines({ exception }: { exception: CurationExceptionDTO }) {
  const external = exceptionExternal(exception);
  const lines = exceptionChangeLines(exception);
  return (
    <ul className={styles.lines}>
      {external === null ? null : (
        <li>
          {exceptionLinkVerb(exception)} MusicBrainz {exception.kind === "recording_match" ? "录音" : "发行"}{" "}
          {external.url === null ? (
            <span className={styles.untrusted}>{external.id}</span>
          ) : (
            <a href={external.url} target="_blank" rel="noopener noreferrer" className={styles.untrusted}>
              {external.id}
            </a>
          )}
        </li>
      )}
      {lines.map((line, index) => (
        <li key={index} className={styles.untrusted}>
          {line}
        </li>
      ))}
    </ul>
  );
}
