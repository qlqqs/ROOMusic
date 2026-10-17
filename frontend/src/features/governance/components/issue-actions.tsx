import { BadgeCheck, PencilLine, Undo2 } from "lucide-react";
import { useLayoutEffect, useRef, useState } from "react";
import { Button } from "../../../shared/ui";
import type { FieldIssueDTO, ReleaseIssuesDTO } from "../api/decoders";
import type { ReleaseCommands } from "../hooks/use-release-commands";
import { reviewableIssueLabels } from "../labels";
import { EditReleaseDialog } from "./edit-release-dialog";
import type { ReviewSubject } from "./release-issues-detail";
import styles from "./issue-actions.module.css";

type ReleaseRef = Pick<ReleaseIssuesDTO, "release_id" | "revision"> & { title: string };

/**
 * 一类可以核对的问题的操作：待处理时“标记为已核对”（带页面读到的依据令牌），已核对或过期时“取消核对”；
 * 过期的核对（核对之后情况变了）两者都有：可以按现在的情况重新核对，也可以取消。不弹确认（随时可以撤销，也不丢数据），
 * 结果提示在详情上方，成功时带“撤销”。按钮的可访问名称带上问题，读屏的按钮列表里能分辨。
 */
export function ReviewAction({ subject, release, commands }: { subject: ReviewSubject; release: ReleaseRef; commands: ReleaseCommands }) {
  const label = reviewableIssueLabels[subject.issue];
  const rowRef = useRef<HTMLDivElement>(null);
  const hadFocus = useRef(false);
  // 核对或取消之后按钮换成另一个（标记为已核对 ↔ 取消核对），被点的按钮已不在页面上，焦点会掉回页面顶部：
  // 这时把焦点带到这一节的标题（tabIndex=-1，不进 Tab 顺序），下一次 Tab 就到新的按钮。
  useLayoutEffect(() => {
    if (!hadFocus.current) return;
    const row = rowRef.current;
    if (row !== null && row.contains(document.activeElement)) return;
    hadFocus.current = false;
    if (document.activeElement === null || document.activeElement === document.body) row?.closest("section")?.querySelector<HTMLElement>("h3")?.focus();
  });
  const pending = commands.state.status === "pending" && commands.state.command.kind === "review" && commands.state.command.issue === subject.issue && commands.state.command.release.id === release.release_id;
  const pendingAction = pending && commands.state.status === "pending" && commands.state.command.kind === "review" ? commands.state.command.action : null;
  const target = { id: release.release_id, title: release.title };
  const submit = (action: "review" | "unreview") => {
    hadFocus.current = rowRef.current?.contains(document.activeElement) ?? false;
    commands.dismiss();
    commands.submit({ kind: "review", release: target, revision: release.revision, issue: subject.issue, action, basis: action === "review" ? subject.basis : undefined });
  };
  const reviewText = pendingAction === "review" ? "正在保存…" : subject.review === null ? "标记为已核对" : "按现在的情况重新核对";
  const unreviewText = pendingAction === "unreview" ? "正在取消…" : "取消核对";
  // 可访问名称用 aria-label 一次写全：看不见的后缀单独成段时，浏览器会在全角冒号前插入空格。
  return (
    <div ref={rowRef} className={styles.row}>
      {subject.status === "open" ? (
        <Button icon={<BadgeCheck size={16} />} busy={pendingAction === "review"} aria-label={`${reviewText}：${label}`} onClick={() => submit("review")}>
          {reviewText}
        </Button>
      ) : null}
      {subject.review === null ? null : (
        <Button variant="text" icon={<Undo2 size={16} />} busy={pendingAction === "unreview"} aria-label={`${unreviewText}：${label}`} onClick={() => submit("unreview")}>
          {unreviewText}
        </Button>
      )}
    </div>
  );
}

/** 字段问题的处理：一个“编辑发行信息”对话框一次改完（与专辑详情里的同一个），成功之后问题页重新读取。 */
export function FieldIssueActions({ fields, release, commands }: { fields: readonly FieldIssueDTO[]; release: ReleaseRef; commands: ReleaseCommands }) {
  const [editing, setEditing] = useState(false);
  // 来源类型与介质类型不能在编辑里修改：只有它们的话没有可以编辑的字段。
  if (fields.every((field) => field.reviewable)) return null;
  return (
    <div className={styles.row}>
      <Button
        icon={<PencilLine size={16} />}
        onClick={() => {
          commands.dismiss();
          setEditing(true);
        }}
      >
        编辑发行信息
      </Button>
      <EditReleaseDialog open={editing} release={{ id: release.release_id, title: release.title }} commands={commands} onClose={() => setEditing(false)} />
    </div>
  );
}
