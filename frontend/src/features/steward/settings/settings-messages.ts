import type { NativeSearchSupport, Protocol, SearchTestCode, ToolSupport } from "../api/decoders";

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

/** 成员的 SearXNG 地址同样只能是公网地址（ADR 0005 第 5 节）。 */
export const SEARCH_EGRESS_DENIED_TEXT = "这个地址不允许连接。成员的 SearXNG 地址只能是公网地址，要用家里自己部署的 SearXNG，请管理员在“成员可以连接的内网地址”里放行。";

/** 保存联网搜索的失败：invalid_input 在这里多半是选了模型自带、而检测没有确认支持。 */
export const searchFailureMessages: Readonly<Record<string, string>> = {
  ...settingsFailureMessages,
  egress_denied: SEARCH_EGRESS_DENIED_TEXT,
  invalid_input: "填写的内容不合要求：地址要以 http:// 或 https:// 开头；选模型自带的搜索要先确认这个模型支持。",
};

/** 测试搜索没有通过的原因。 */
export const searchTestFailureTexts: Readonly<Record<SearchTestCode, string>> = {
  search_not_configured: "还没有配置可以测试的搜索服务：选 SearXNG 或 Tavily 并保存后再测试。",
  searxng_json_disabled: "这个实例没有开启 JSON 输出。在 SearXNG 的设置里把 json 加进 search.formats，重启后再试。",
  search_key_invalid: "Tavily 不认这个密钥，请检查后重新填写。",
  search_quota_exceeded: "搜索服务的额度用完了，或者超出了套餐的限制。",
  search_unreachable: "连不上搜索服务，请检查地址是否正确、服务有没有在运行。",
  egress_denied: SEARCH_EGRESS_DENIED_TEXT,
};

export function searchTestFailureText(code: string): string {
  return (searchTestFailureTexts as Readonly<Record<string, string>>)[code] ?? "搜索没有成功，请稍后再试。";
}

export const protocolLabels: Readonly<Record<Protocol, string>> = {
  openai: "OpenAI 兼容接口",
  messages: "Anthropic Messages 接口",
  responses: "OpenAI Responses 接口",
};

export const toolSupportLabels: Readonly<Record<ToolSupport, string>> = {
  supported: "支持",
  unsupported: "这个模型不支持",
  unconfirmed: "还没有确认",
};

export const nativeSearchLabels: Readonly<Record<NativeSearchSupport, string>> = {
  supported: "支持",
  unsupported: "不支持",
  unknown: "还没有确认",
};

export const probeStepLabels = { endpoint: "检查接口地址", models: "读取模型列表", tools: "试调用工具", responses: "检查 Responses 接口", native_search: "试用自带搜索" } as const;

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
