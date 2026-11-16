import type { Protocol, ToolSupport } from "../api/decoders";

// 设置页的文案：保存与检测的失败、协议与工具能力的名称、探测失败的原因（合同 4.2、4.3 与第 6 节的分类）。

/** 成员只能用公网地址；内网地址要管理员放行（ADR 0005 第 5 节）。 */
export const EGRESS_DENIED_TEXT = "这个地址不允许连接。成员的模型接口只能是公网地址，要用家里自己部署的模型，请管理员在“成员可以连接的内网地址”里放行。";

export const settingsFailureMessages: Readonly<Record<string, string>> = {
  egress_denied: EGRESS_DENIED_TEXT,
  provider_timeout: "检查接口地址时超时了，请稍后重试。",
  provider_unreachable: "解析不了这个接口地址，请检查地址是否正确。",
  invalid_input: "填写的内容不合要求：地址要以 http:// 或 https:// 开头，名字最多 20 个字。",
  steward_disabled: "管理员暂时关闭了成员使用管家。",
};

export const protocolLabels: Readonly<Record<Protocol, string>> = {
  openai: "OpenAI 兼容接口",
  messages: "Anthropic Messages 接口",
};

export const toolSupportLabels: Readonly<Record<ToolSupport, string>> = {
  supported: "支持",
  unsupported: "这个模型不支持",
  unconfirmed: "还没有确认",
};

export const probeStepLabels = { endpoint: "检查接口地址", models: "读取模型列表", tools: "试调用工具" } as const;

const failureKindLabels: Readonly<Record<string, string>> = {
  egress_denied: "地址不允许连接",
  timeout: "超时",
  unreachable: "连不上",
  authentication: "认证失败，请检查密钥",
  rate_limit: "限流或额度不足",
  model_not_found: "模型不存在",
  invalid_response: "返回的内容无法识别",
  stream_interrupted: "回应中断了",
  tool_unsupported: "模型不支持调用工具",
  other: "出错了",
};

export function failureKindLabel(kind: string): string {
  return failureKindLabels[kind] ?? "出错了";
}
