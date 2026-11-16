# 管家 C8 执行计划

分支 `steward-c8-web`，依赖 C5 已合入（工具合同），与 C6、C7 没有代码依赖，但共用运行时文件，排在 C7 之后做。
后端实现 `gpt-6-astra` high（两个子代理先后做：先搜索、抓取、MusicBrainz；后 Responses 与自带搜索），
测试由另一个子代理；前端 Opus 5.5（high）。

- [x] 1 把调研结果存成 `research/web-sources.md`。
- [x] 2 后端甲：迁移 0056、搜索设置、`web_search`、`fetch_page`、MusicBrainz 两个工具、`Available`。
- [x] 3 后端乙：Responses 适配器与探测；Messages 服务端搜索；`provider_blocks`；事件。
- [x] 4 后端测试；规范新增 `steward-web-contracts.md`，运行时合同与 ADR 0005（D5 补充、robots、Responses）同步；
      父任务 S3 的修改记进父任务 PRD。
- [x] 5 前端：搜索设置、Responses 选项、搜索条目与来源列表；组件测试。
- [x] 6 独立审查（只读，重点是出站边界与密钥），修复，提交。

## 验证

- 后端：`go test ./internal/steward/web/... ./internal/steward/provider/...`，`runtime`、`httpapi` 里新增与受影响的用例，迁移用例。
- 前端：`tsc --noEmit`、改动文件 lint、`agent-settings` 与 `steward*` 测试。
- 真实的搜索服务、MusicBrainz 与模型端点都不在自动化里请求；需要用户提供密钥的试用单独记录为未执行。

## 风险点

- 新增依赖只允许 `golang.org/x/net`、`golang.org/x/text`；需要别的依赖时停下来问。
- 第三方接口细节以实现时查到的文档为准，训练记忆里的字段名不可靠。

## 执行记录（2026-11-16）

- 后端甲、乙完成后，独立只读审查未发现出站边界、密钥、不可信标记、跨端点回放方面可利用的缺口（除构建与 vet 外为读代码推断），
  列出 2 条中等、8 条低级问题；测试第一轮 89 个叶子用例 88 通过，1 个失败（探测把“工具不支持”归为 `unknown`）。
- 修复 F1–F10：探测归类；四个联网工具按用户限流（`fetch_page` 30 次/分、MusicBrainz 两工具合计 20 次/分、`web_search` 30 次/分）；
  原始块回放归属加模型名；Responses 自带搜索 `max_tool_calls` 5；搜索结果、引用、标题、查询词、`provider_blocks` 上限；
  SearXNG 地址只在相关保存时预检；自闭合 `script`/`style`；Tavily 与 MusicBrainz 客户端改为仅公网；日志固定字段；公网判断补五个网段。
- 测试第二轮（收窄）：152 个叶子用例通过，0 失败；3 项因未配置数据库跳过，沿用第一轮集成结果。依赖规则登记 `steward/web`。
  记录在 `temp/c8-backend-tests/commands-and-results.txt` 与 `round2-commands-and-results.txt`。
- 前端：子代理报告 `tsc`、eslint、Vitest 15 个文件 314 条通过；主代理运行 `src/test/api-contract.test.ts`（656 通过、1 跳过，
  登记 8 个新样例）与 `e2e/steward-web.spec.ts`（3 条通过）。探测失败步骤新增 `responses`、`native_search` 两个取值，前端已接。
- 与设计的差异：网页正文受注册表 64 KiB 的工具结果上限约束，不是设计里的 100 KiB；“自带搜索”的手动修改放在检测结果面板。
- 未执行：前后端整套测试；真实模型端点、搜索服务、MusicBrainz 的试用；修复后的再次独立审查。
- 只有真实端点能确认：Messages 自带搜索的多轮回放与暂停续跑；Responses 无状态历史回传在兼容代理上的行为；
  强制审查调用若附带推理输出，C7 会判为不可用并转人工确认。
