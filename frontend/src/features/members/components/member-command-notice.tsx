import { describeFailure, isHttpFailure } from "../../../shared/api";
import { Button, Notice } from "../../../shared/ui";
import type { MemberCommand, MemberCommandState } from "../hooks/use-member-commands";
import { memberFailureMessages } from "./member-messages";

type MemberCommandNoticeProps = {
  state: MemberCommandState;
  /** 新建成员的结果显示在表单旁，其他操作的结果显示在列表上方。 */
  scope: "create" | "list";
  onDismiss: () => void;
};

function successText(command: MemberCommand): string {
  switch (command.kind) {
    case "create":
      return `已创建成员“${command.username}”。该成员可以用这个用户名和你设置的密码登录。`;
    case "disable":
      return `已停用成员“${command.member.username}”。该成员的全部会话已被撤销，重新启用之前无法登录。`;
    case "enable":
      return `已重新启用成员“${command.member.username}”。该成员可以再次登录。`;
    case "revoke":
      return `已撤销成员“${command.member.username}”的全部会话。该成员需要重新登录。`;
  }
}

/**
 * 成员命令的结果：成功、明确的拒绝（4xx）与结果不确定（连接中断、无效响应、5xx）。
 * 成员写操作没有幂等保证，所以不提供“重试”：结果不确定时列表已经刷新，请用户核对后再决定。
 * 用户名重复显示在用户名输入框旁，这里不重复。
 */
export function MemberCommandNotice({ state, scope, onDismiss }: MemberCommandNoticeProps) {
  if (state.status !== "succeeded" && state.status !== "failed") return null;
  if ((state.command.kind === "create") !== (scope === "create")) return null;
  const dismiss = (
    <Button variant="text" onClick={onDismiss}>
      关闭
    </Button>
  );

  if (state.status === "succeeded") {
    return (
      <Notice tone="success" action={dismiss}>
        {successText(state.command)}
      </Notice>
    );
  }

  if (state.command.kind === "create" && isHttpFailure(state.failure, 409, "username_taken")) return null;
  const description =
    state.failure === null
      ? { message: "发生了未预期的错误，请刷新页面后重试。", requestId: null }
      : describeFailure(state.failure, { context: "write", messages: memberFailureMessages });
  return (
    <Notice tone="error" title={state.uncertain ? "结果未确认" : "操作没有完成"} requestId={description.requestId} action={dismiss}>
      <p>{description.message}</p>
      {state.uncertain ? <p>列表已刷新。请核对成员的当前状态，再决定是否重新操作。</p> : null}
    </Notice>
  );
}
