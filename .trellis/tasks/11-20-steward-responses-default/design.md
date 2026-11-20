# 设计：探测优先选 Responses

## 边界

只改 `provider.Probe` 的 OpenAI 兼容分支（`backend/internal/steward/provider/probe.go`）。`httpapi` 的探测处理、设置合并、运行时协议选择、
数据库结构、`ProbeResult` 的字段都不变；前端预期不改代码。

## 探测流程（OpenAI 兼容端点，`model` 非空）

模型列表仍用 `openai` 适配器取得，之后：

1. 用 Responses 适配器做工具调用探测（复用 `probeTools`，同一提示词与 1024 上限）。
2. 结论为 `supported`：`Protocol=responses`、`ToolSupport=supported`、`ResponsesAvailable=true`，继续第 4 步。
3. 其他情况回退：`Protocol=openai`，在 Chat Completions 下做工具调用探测，结论与失败按现有规则写入（`step: "tools"`）。
   - Responses 请求出错：记一条 `step: "responses"`、`protocol: "responses"` 的失败，`ResponsesAvailable=false`，不做自带搜索探测。
   - Responses 请求完整结束但没有调用工具：`ResponsesAvailable=true`，不记失败，继续第 4 步。
4. `ResponsesAvailable=true` 时用 Responses 适配器做自带搜索探测，失败记 `step: "native_search"`（现有行为）。

Responses 阶段的失败一律用 `step: "responses"`，不用 `"tools"`：`httpapi/probe.go` 遇到 `step == "tools"` 且不是“不支持工具”的失败会放弃保存，
回退路径不能触发它。原来的 `probeResponses`（只回复 OK）被第 1 步取代，删除。

`model` 为空、`messages` 协议、全部协议列表失败的分支不变。

## 调用次数

| 路径 | 模型调用 |
| --- | --- |
| Responses 工具通过 | Responses 工具、自带搜索，共 2 次（原 3 次） |
| Responses 出错 | Responses、Chat 工具，共 2 次 |
| Responses 通但没调用工具 | Responses、Chat 工具、自带搜索，共 3 次 |

## 与运行时的关系

- 保存的 `protocol=responses` 时运行时直接用 Responses，工具结论取探测值，不经过 `settings.go:305` 的临时切换。
- 回退到 `openai` 的端点保持现状：自带搜索为支持时临时切到 Responses，工具结论按未确认处理。

## 兼容

- 已保存的设置不迁移；重新检测后才变成 `responses`。
- `probe_result` 的 JSON 形状不变，旧报告仍可解码。
- 只支持 Chat Completions 的服务商走回退路径，结果与改动前相同，只是 `failures` 里多一条 Responses 失败（改动前同样会记）。

## 风险与回滚

- Responses 工具探测通过但真实对话在某些服务商上有差异：用户可在“接口类型”手动改回 Chat Completions。
- 回滚：还原 `probe.go` 与对应测试、规范即可，没有数据变更。
