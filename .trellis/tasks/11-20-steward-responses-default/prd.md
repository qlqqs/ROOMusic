# 管家默认优先使用 Responses 协议

## 目标

OpenAI 兼容端点在 Responses 接口可用且能调用工具时，探测把协议定为 `responses`；不满足时回退到 Chat Completions（`openai`）。
用户不必手动选协议，也不会再出现“手动选了 Responses，重新探测后被改回 `openai`”。

价值：2026-11-20 真实端点试用（`gpt-6.2-sol`，约 80 个工具）中，`responses` 每次请求约 0.9 万输入 token，`openai` 约 1.5 万（未做严格对照）；
开不开自带搜索都走同一种协议。

## 已确认的事实

- 探测只在 `openai`、`messages` 里选第一个能列出模型的协议，从不选 `responses`（`backend/internal/steward/provider/probe.go:69`）。
- 选中 `openai` 后附带检查 Responses：只发一句“请只回复 OK”（`probe.go:329`），不测工具调用；结果记在 `responses_available`，后端不使用，
  前端只用来给“Responses”选项加提示（`frontend/src/features/steward/settings/probe-result.tsx:38`）。
- 探测成功后无条件把 `protocol` 写成识别出的协议、`overrides` 重置为 `{}`（`backend/internal/steward/httpapi/probe.go`，运行时合同 4.3 第 6 条）。
- 手动改接口类型写进 `overrides.protocol`，并清掉探测结论（运行时合同 4.2 规则 2）；界面提示重新检测，而重新检测又会清空 `overrides`。
- 运行时在“协议为 `openai`、搜索来源为自带搜索且结论为支持”时临时切到 Responses，并把工具结论当作未确认（`backend/internal/steward/httpapi/settings.go:305`）。
- 规范写明“默认仍为 openai”（`.trellis/spec/backend/steward-runtime-contracts.md:845`、联网合同）。
- 试用端点上 Responses 的工具调用、确认流程、独立审查、自带搜索均实测可用；OpenAI 官方端点与其他兼容服务商没有实测。

## 需求

- R1 探测 OpenAI 兼容端点时，先在 Responses 下做工具调用探测；结论为 `supported` 时协议定为 `responses`，`tool_support` 取这次结论，
  `responses_available` 为 true，再做自带搜索探测。
- R2 Responses 请求失败或工具结论不是 `supported` 时，协议定为 `openai`，在 Chat Completions 下做工具调用探测；Responses 的失败留在 `failures` 里。
  Responses 请求完整结束但没有调用工具时 `responses_available` 仍为 true，且不记失败。
- R3 `messages` 协议端点（含地址预设为 Anthropic 的）行为不变。
- R4 成功路径的模型调用次数不超过现在（现在是 Chat 工具探测、Responses 连通、自带搜索三次）。
- R5 运行时“自带搜索时从 `openai` 临时切到 Responses”的逻辑保留，供回退到 `openai` 的端点使用。
- R6 前端设置页在探测结论为 `responses` 时显示正确：现有逻辑按 `report.protocol` 与 `responses_available` 取值，预期不用改代码，补一条用例确认。
- R7 规范同步：运行时合同第 6 节的探测步骤、联网合同里“默认仍为 openai”与探测顺序的表述、前端规范里检测结果的说明。

## 验收标准

- A1 受控端点同时支持 Responses 工具调用时，探测保存 `protocol=responses`、`tool_support=supported`、`responses_available=true`。
- A2 受控端点的 Responses 请求出错（如 404）时，探测保存 `protocol=openai`，工具结论来自 Chat Completions，`failures` 里有 `step: "responses"` 一条；
  Responses 能通但没有调用工具时同样保存 `protocol=openai`，`responses_available=true`，不记 Responses 失败。
- A3 `messages` 端点的探测结果与改动前一致。
- A4 试用实例上对 `api.qlqq.de` 重新探测得到 `protocol=responses`，随后的只读对话、确认写入、steward 模式审查、自带搜索各跑通一次。
- A5 前端设置页在 `protocol=responses` 且已确认时不再显示“检测没有确认这个接口支持”。

## 不在范围内

- 已保存设置的自动迁移：已有用户重新点一次检测才切换。
- 错误码细分（模型不存在、抓取 403/404）、assistant 模式提示词。
- OpenAI 官方端点与其他服务商的实测。

## 关键决定

- D1 探测说了算（qlqq，2026-11-20）：探测成功后仍把 `protocol` 写成识别出的协议并把 `overrides` 重置为 `{}`，不保留手动选的接口类型。
  理由：手动修改在现有设计里是“对检测结果的修改”，重新检测即以检测为准；默认优先 Responses 后“被改回 openai”不再出现；
  Responses 工具调用没通过时保留手动选择反而不可用。运行时合同 4.2、4.3 第 6 条不变。
- D2 默认优先 Responses（qlqq，2026-11-20）：见 R1、R2。
- D3 `max_tool_calls` 已在 `5393fac6` 去掉，本任务的自带搜索探测沿用。

## 规划决定记录

- 路线：人工选择。选择者：qlqq。
- 结果：2026-11-20 批准最终规划摘要，按 `design.md`、`implement.md` 实现，无修改意见。
- 理由：范围只有探测流程一处，无数据库变更，回退路径和模型调用次数都不劣于现状。
