import { CircleAlert, CircleStop } from "lucide-react";
import { describeFailure } from "../../../shared/api";
import { Button } from "../../../shared/ui";
import type { RunEndDTO } from "../api/decoders";
import type { RunEnd } from "../run/run-store";
import { AgentSettingsLink } from "./settings-link";
import styles from "./thread.module.css";

type Ended = RunEnd | { kind: "incomplete" };

/** 模型接口失败的原因（error_code 与 error_kind 按合同第 6 节的分类表）。 */
function providerReason(end: RunEndDTO): string {
  switch (end.error_code) {
    case "provider_timeout":
      return "模型接口没有及时回应";
    case "egress_denied":
      return "不允许连接这个接口地址";
    case "provider_no_tool_support":
      return "这个模型不支持调用工具";
  }
  switch (end.error_kind) {
    case "authentication":
      return "模型接口认证失败";
    case "rate_limit":
      return "模型接口限流或额度不足";
    case "model_not_found":
      return "模型接口里没有这个模型";
    case "invalid_response":
      return "模型接口返回的内容无法识别";
    case "stream_interrupted":
      return "模型接口的回应中断了";
    case "unreachable":
      return "连不上模型接口";
    default:
      return "模型接口出错了";
  }
}

type Note = { tone: "warning" | "calm"; title: string; detail?: string | undefined; action?: "retry" | "continue"; settings?: boolean };

function noteOf(ended: Ended, hasSteps: boolean): Note | null {
  const saved = hasSteps ? "前面查到的资料已经保存。" : "";
  switch (ended.kind) {
    case "incomplete":
      return { tone: "warning", title: "这次回答没有完成", action: "continue" };
    case "disconnected":
      // 运行不随连接停止（C10）：重新接回也没接上时，回答可能还在后台继续。
      return { tone: "warning", title: "连接断了，没能接上这次回答", detail: `${saved}回答可能还在后台继续，可以刷新页面查看最新的结果。`, action: "continue" };
    case "request_failed":
      return { tone: "warning", title: "没有发出去", detail: describeFailure(ended.failure, { context: "write", messages: requestMessages }).message };
    case "done": {
      const end = ended.end;
      switch (end.reason) {
        case "completed":
        case "awaiting_confirmation":
        case "review_unavailable":
          // 等用户确认方案，或审查不可用（C7）：方案与审查的区块写明了下一步，不另加说明。
          return null;
        case "cancelled":
          return { tone: "calm", title: "已停止" };
        case "step_limit":
          return { tone: "warning", title: "查询步数用完了，回答还没有完成", detail: "一次回答最多请求模型 24 次。", action: "continue" };
        case "token_limit":
          return { tone: "warning", title: "用量到了上限，回答还没有完成", detail: "这次回答用掉的 token 到了上限，或者模型一次输出太长。", action: "continue" };
        case "provider_error": {
          const message = end.message ?? "";
          const status = end.status_code === undefined ? "" : `接口返回 ${end.status_code}${message === "" ? "" : `：${message}`}。`;
          const detail = (status === "" && message !== "" ? `${message}。` : status) + saved;
          return { tone: "warning", title: `${providerReason(end)}，这次回答没有完成`, detail: detail || undefined, action: "retry", settings: true };
        }
        case "internal_error":
          return { tone: "warning", title: "管家运行时出了内部错误，这次回答没有完成", detail: saved || undefined, action: "retry" };
        default:
          // 没有 reason：运行中存储失败（合同第 5 节）。
          return { tone: "warning", title: "保存对话时出错，这次回答没有完成", detail: saved || undefined, action: "retry" };
      }
    }
  }
}

// 开始流之前的拒绝（合同 4.5 与第 10 节）。
const requestMessages: Readonly<Record<string, string>> = {
  conversation_busy: "这个对话正在回答，等它结束或先停止。",
  steward_not_configured: "还没有配置好模型，请先到设置里检查。",
  provider_no_tool_support: "当前模型不支持调用工具，请换一个模型。",
  egress_denied: "不允许连接这个接口地址。成员只能用公网地址，内网地址需要管理员放行。",
  provider_timeout: "模型接口没有及时回应，请稍后重试。",
  provider_unreachable: "连不上模型接口，请检查接口地址。",
  nothing_to_continue: "上一次回答已经完成，没有需要继续的内容。",
  steward_disabled: "管理员暂时关闭了成员使用管家。",
  not_found: "这个对话不存在或已经删除。",
  // 503 的原因不显示服务端的说明，这里写明；同时运行的会话超过上限（steward_run_limit）等 409 显示服务端给的原因。
  steward_shutting_down: "服务正在关闭，请稍后再试。",
};

/**
 * 运行没有正常结束时跟在过程后面的说明：原因、接口返回的状态与信息，以及下一步（重试、继续、检查模型设置）。
 * 重试与继续都从现有历史接着跑，不重复追加用户消息；只在落库内容确实没有完成时给出（否则继续接口必得 nothing_to_continue）。
 * 说明文字是 live region，按钮放在它外面。
 */
export function RunEnded({ ended, hasSteps, canContinue, busy, onContinue }: { ended: Ended; hasSteps: boolean; canContinue: boolean; busy: boolean; onContinue: () => void }) {
  const note = noteOf(ended, hasSteps);
  if (note === null) return null;
  const Icon = note.tone === "calm" ? CircleStop : CircleAlert;
  const action = canContinue ? note.action : undefined;
  return (
    <div className={styles.ended} data-tone={note.tone}>
      <Icon size={16} aria-hidden="true" />
      <div>
        <div role="status">
          <p>{note.title}</p>
          {note.detail === undefined ? null : <p className={styles.endedDetail}>{note.detail}</p>}
        </div>
        {action === undefined && note.settings !== true ? null : (
          <div className={styles.endedActions}>
            {action === undefined ? null : (
              <Button variant="primary" busy={busy} onClick={onContinue}>
                {action === "retry" ? "重试" : "继续"}
              </Button>
            )}
            {note.settings === true ? <AgentSettingsLink className={styles.quietLink}>检查模型设置</AgentSettingsLink> : null}
          </div>
        )}
      </div>
    </div>
  );
}
