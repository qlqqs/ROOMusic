# 执行计划

分三批，每批"实现代理 → 测试代理 → 主代理审阅合入"，合入前跑依赖门禁和改动包整包单测（见记忆 merge-run-dependency-gate）。

## 第一批：只读工具与技能（R3、R4、R5）

实现（sol，high）：
1. `library`：导出 `PathExcluded`；新增根内安全列目录与打开的封装（`os.OpenRoot`）。
2. `library/parser`：导出文本编码识别；新增接受已打开文件的音频解析入口。
3. 管家工具 `list_library_directory`、`read_library_file`（放在 `library/stewardtools`），注册到聊天与 MCP。
4. `steward/skills` 与 `load_skill`，系统提示追加技能列表；先放 `album-curation` 的完整内容。
5. 更新 `.trellis/spec/backend/steward-runtime-contracts.md` 工具清单与审查证据例外。

测试（sol，high）：
- 路径边界：`..`、绝对路径、指向目录外的文件与目录链接、内置忽略、用户排除及其子孙、被排除路径本身返回 `not_found`。
- 列目录归属：普通音频、CUE 分轨、附件；分页；输出不含绝对路径。
- 读文件：GBK 与 UTF-8 文本、超限、音频标签不含封面与歌词、图片尺寸、非管理员被拒。
- 技能：列表按身份过滤、加载未知技能报错。

## 第二批：建立对应（R1、R6）

实现（astra，high）：`program.SelectExternal`、工具 `match_release_external` 与预览、组级结论、录音分支、拒绝码。

测试（sol，high）：
- 版本对应成功后外部对应、自动字段、工作状态正确，worker 随后补署名与封面任务入队。
- 曲数或时长不符返回 `insufficient_corroboration`；已对应返回 `already_matched`；被"不再提出"返回 `suppressed`。
- 有专辑标签的部分专辑认到发行组；散曲认到录音。
- 与后台 worker 并发：同一发行同时写入只有一方成功；结束等待中的待定项。
- 逐步撤销。

## 第三批：重新归组（R2）

实现（astra，high）：`SubmitStewardGrouping`、工具 `regroup_local_files` 与预览、人工结构判断扩展、立即确认扫描。

测试（sol，high）：
- 混放目录分成两张、Bonus 拆成独立一张，扫描后发行与曲目归属正确，曲目 ID 不变。
- 受保护发行返回 `protected`；撤销原对应后可以归组。
- 覆盖不全、文件重复、同碟曲号重复、与真实碟轨号冲突各自被拒。
- 之后本地辅助不改这次归组；撤销后恢复原样并不再提出。

## 前端（主代理）

`tool-labels.ts` 增加 5 个工具名，跑 `src/features/steward` 相关用例、lint、typecheck。

## 收尾

- 更新 `.trellis/spec/backend/curation-contracts.md`（聊天入口的对应与归组）和 `steward-runtime-contracts.md`。
- 合成曲库上跑一次真实模型试用：一张冲突专辑、一张混放目录、一张网络专辑，按需进行。
