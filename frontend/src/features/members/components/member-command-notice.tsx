import { describeFailure, isHttpFailure } from "../../../shared/api";
import { Button, Notice } from "../../../shared/ui";
import type { MemberCommand, MemberCommandState } from "../hooks/use-member-commands";
import { memberFailureMessages, resetPasswordFailureMessages } from "./member-messages";

type MemberCommandNoticeProps = {
  state: MemberCommandState;
  /** 新建成员失败显示在新建对话框里；成功和其他操作的结果显示在列表上方。 */
  scope: "create" | "list";
  onDismiss: () => void;
};

function successText(command: MemberCommand): string {
  switch (command.kind) {
    case "create":
      return `已创建成员“${command.username}”。`;
    case "disable":
      return `已停用成员“${command.member.username}”，对方已退出登录。`;
    case "enable":
      return `已重新启用成员“${command.member.username}”。`;
    case "revoke":
      return `已撤销成员“${command.member.username}”的全部会话，对方需要重新登录。`;
    case "reset-password":
      return `已重置“${command.member.username}”的密码。`;
  }
}

/**
 * 成员命令的结果：成功、明确的拒绝（4xx）与结果不确定（连接中断、无效响应、5xx）。
 * 成员写操作没有幂等保证，所以不提供“重试”：结果不确定时列表已经刷新，请用户核对后再决定。
 * 用户名重复显示在用户名输入框旁，这里不重复。
 */
export function MemberCommandNotice({ state, scope, onDismiss }: MemberCommandNoticeProps) {
  if (state.status !== "succeeded" && state.status !== "failed") return null;
  const inDialog = state.status === "failed" && state.command.kind === "create";
  if (inDialog !== (scope === "create")) return null;
  const dismiss = (
    <Button onClick={onDismiss}>
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
  const resetting = state.command.kind === "reset-password";
  const description =
    state.failure === null
      ? { message: "发生了未预期的错误，请刷新页面后重试。", requestId: null }
      : describeFailure(state.failure, { context: "write", messages: resetting ? resetPasswordFailureMessages : memberFailureMessages });
  // 列表看不出密码有没有换：重置的结果不确定时直接请用户再重置一次（再设一次同样的密码没有坏处）。
  if (resetting && state.uncertain) {
    return (
      <Notice tone="error" title="结果未确认" requestId={description.requestId} action={dismiss}>
        不确定密码有没有重置好，可以再重置一次。
      </Notice>
    );
  }
  return (
    <Notice tone="error" title={state.uncertain ? "结果未确认" : "操作没有完成"} requestId={description.requestId} action={dismiss}>
      <p>{description.message}</p>
      {state.uncertain ? <p>列表已刷新，请看一下成员现在的状态再操作。</p> : null}
    </Notice>
  );
}
