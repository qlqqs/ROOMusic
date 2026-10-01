import type { ReactNode } from "react";
import type { FailureContext } from "../api";
import { describeFailure, failureOf } from "../api";
import { Notice } from "./notice";

type FailureNoticeProps = {
  /** 查询或写操作抛出的错误；不是 ApiError 时按未预期的错误显示。 */
  error: unknown;
  context?: FailureContext;
  /** 按错误码覆盖后端错误的文案。 */
  messages?: Readonly<Record<string, string>>;
  title?: string;
  /** 下一步操作，例如“重试”。 */
  action?: ReactNode;
};

/** 把请求失败显示为带请求 ID 的行内错误，所有页面的错误状态共用同一份文案规则。 */
export function FailureNotice({ error, context, messages, title, action }: FailureNoticeProps) {
  const failure = failureOf(error);
  const description =
    failure === null
      ? { message: "发生了未预期的错误，请刷新页面后重试。", requestId: null }
      : describeFailure(failure, { context, messages });
  return (
    <Notice tone="error" {...(title === undefined ? {} : { title })} requestId={description.requestId} action={action}>
      {description.message}
    </Notice>
  );
}
