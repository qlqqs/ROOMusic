# 管家 C8 执行计划

分支 `steward-c8-web`，依赖 C5 已合入（工具合同），与 C6、C7 没有代码依赖，但共用运行时文件，排在 C7 之后做。
后端实现 `gpt-6-astra` high（两个子代理先后做：先搜索、抓取、MusicBrainz；后 Responses 与自带搜索），
测试由另一个子代理；前端 Opus 5.5（high）。

- [ ] 1 把调研结果存成 `research/web-sources.md`。
- [ ] 2 后端甲：迁移 0056、搜索设置、`web_search`、`fetch_page`、MusicBrainz 两个工具、`Available`。
- [ ] 3 后端乙：Responses 适配器与探测；Messages 服务端搜索；`provider_blocks`；事件。
- [ ] 4 后端测试；规范新增 `steward-web-contracts.md`，运行时合同与 ADR 0005（D5 补充、robots、Responses）同步；
      父任务 S3 的修改记进父任务 PRD。
- [ ] 5 前端：搜索设置、Responses 选项、搜索条目与来源列表；组件测试。
- [ ] 6 独立审查（只读，重点是出站边界与密钥），修复，提交。

## 验证

- 后端：`go test ./internal/steward/web/... ./internal/steward/provider/...`，`runtime`、`httpapi` 里新增与受影响的用例，迁移用例。
- 前端：`tsc --noEmit`、改动文件 lint、`agent-settings` 与 `steward*` 测试。
- 真实的搜索服务、MusicBrainz 与模型端点都不在自动化里请求；需要用户提供密钥的试用单独记录为未执行。

## 风险点

- 新增依赖只允许 `golang.org/x/net`、`golang.org/x/text`；需要别的依赖时停下来问。
- 第三方接口细节以实现时查到的文档为准，训练记忆里的字段名不可靠。
