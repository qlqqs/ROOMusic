# 管家 C5 执行计划

分支 `steward-c5-personal`。后端实现交 Codex（`gpt-6-astra` high），测试交另一个子代理（`gpt-6.1-sol` high）；
前端交 Opus 5.5（high）子代理；子代理不提交 Git。

- [x] 1 后端：调用入口放开 `personal`；三个 `stewardtools` 包与注册；迁移 0053；上下文的输入、落库与重放。
- [x] 2 后端测试与接口样本；规范 `steward-runtime-contracts.md` 第 2–5、7、9 节同步。
- [x] 3 前端：上下文标签与请求体、工具标签、查询失效；组件测试。
- [x] 4 独立审查（trellis-check，只读），修复后提交。

## 验证

- 后端：`go test` 收窄到 `./internal/steward/tool/...`、三个 `stewardtools` 包、`./internal/steward/runtime/...`、
  `./internal/steward/httpapi/...` 与 `./internal/steward/mcp/...` 里新增和受影响的用例，以及迁移与依赖规则用例。
- 前端：`tsc --noEmit`、改动文件 lint、`steward*` 测试文件。
- 集成测试用独立 Compose 项目名与合成数据，模型端点用本地假服务。

## 风险点

- `zones.Service.Command` 不幂等、不重试：工具失败时不自动重试。
- `app/steward.go`、依赖规则测试是共享文件，只追加。

## 执行记录

- 2026-11-16 后端实现（Codex `gpt-6-astra`）：21 个工具（设计表里写 20 个是笔误）、迁移 0053、上下文。与设计的出入：
  按专辑播放复用 `LoadReleaseMedia` 的曲序；删除历史是删除本人该曲目的全部播放记录；删除歌单没有恢复令牌。
- 后端测试（另一个子代理）：单元 21 个通过；集成首轮 134 通过、6 失败，失败指向 `edit_queue` 对不存在的条目返回成功，
  修复后 6 个子用例通过。
- 前端（Opus 5.5 high 子代理）：取数与查询失效放在 `app/shell/steward-host.tsx`（规范不允许 steward 直接导入其他 feature）；
  分区没有曲目时第二个标签显示播放设备。它汇报 `tsc --noEmit`、改动文件 lint 通过。
- 独立审查（只读）：未发现跨用户读写、权限绕过或标记转义绕过；确认 1 个缺陷（继续时把点掉的上下文写回），5 个疑点。
  处理：继续接口不再接受 `context`；工具不再额外要求源分区在线；输入校验说明原样到达模型；空专辑返回 `no_playable_tracks`；
  写工具输出改为计数与摘要（`zones.CommandResult` 加了不进 JSON 的 `AffectedEntries`）；空上下文存 NULL。
  修复后的测试更新：数据库用例 103 个、单元 3 个通过，含 `zones` 包 4 个受影响的既有用例；前端 14 个文件 193 个用例通过（子代理汇报）。
  主代理运行：`src/test/api-contract.test.ts` 644 通过（登记 `steward/invalid-context`）。
- 未做的验证：没有在浏览器里看过标签行，没有跑端到端与全量测试；A1（真实浏览器里播放随工具变化）只有后端集成测试与组件测试覆盖；
  修复之后没有再做独立审查；没有接触真实模型端点。
- 留给 C6：MCP 读写令牌在列表里能看到 `shared` 工具的判定，随 C6 一并处理。
