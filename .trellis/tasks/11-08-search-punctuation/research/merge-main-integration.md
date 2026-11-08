# T 线合并 main 整合记录（2026-11-08）

> 后续裁决已授权并完成三个格式的最小接线修复；首次失败记录保留为历史，最新验证见文末。

## 边界与状态

在已执行的 `git merge --no-commit --no-ff main` 上原地整合；备份引用为 `scan-t-tags-pre-merge-main`。未提交、未 abort/reset，未操作其他 worktree，未启动数据库。任务维持 `in_progress`，数据库验收与最终提交交主会话。

## 冲突处理

- `.trellis/spec/backend/library-scan-contracts.md`：原地保留 S/M/O/T 各主题；CUE 轨号补充与 T 的标签多值/署名规则合并；碟目录、文件名/格式归组、古典作品分别列章；保留 main 的验证章节与 T 的补充验证链接，没有拆文件。旧逗号拆名示例改用明确的 ` / ` 分隔符，避免与 T 已批准的拉丁“姓, 名”保护矛盾。
- `backend/internal/catalog/read.go`：候选种类白名单保留 `format_split`；读取保留 M 的 `track_display_durations`，以及 T 的作品字段、署名 `detail` 与艺人引用；`source` 保持入库，既定 REST 不公开该字段。
- `backend/internal/platform/postgres/migrations_rollback_integration_test.go`：双方夹具合并；S/M/O 的 34–37 保留，T 的夹具、升级前数据分支改到 38–40。保留 0035 下行不撤销目录已改路径及 revision 的期望。夹具 UUID 中的数字是身份，不是迁移号，保持不动。
- `backend/cmd/roomusic/web/index.html`：临时取 T 侧解除冲突，随后由合并后的前端源码统一构建替换；产物新增/删除全部留在工作区。

## 迁移与版本

迁移只改文件名，SQL 内容未变：

| 功能 | 合入编号及文件（各含 `.sql`、`.down.sql`） |
| --- | --- |
| 署名角色 | `backend/migrations/0038_credit_roles` |
| 古典作品 | `backend/migrations/0039_classical_works` |
| 搜索忽略标点 | `backend/migrations/0040_search_punctuation` |

连续序列为 0001–0040，上下行配对；main 的 0034–0037 未改。引用同步涉及回滚测试、`artist-contracts.md`、`search-contracts.md`、`library-scan-contracts.md`、`library-scan-verification.md`，以及⑦⑧⑬任务的 `design.md` / `implement.md`。⑥⑦⑧⑬任务设计和实施文档补充 O/T 合并后的版本说明。

`backend/internal/catalog/internal/artistgraph/state.go` 的 `Version` 从 3 升到 4：O/T 两线曾各自使用 3，整合必须再次触发回填。`searchindex.ProjectionVersion=2`、`textnorm.Version=1` 保持。运行时引用门禁使用共享常量；没有发现要同步的硬编码当前版本测试，历史迁移版本不改。

## 交叉检查

完整调用链与行号见 [交叉语义审查](./merge-semantic-review.md)。发现接入缺口只报告，不改产品规则。

- **确认缺口**：APE、WavPack、AIFF 的入口都未调用 `finalizeTags`。专辑艺人、作品/乐章、歌词头部署名、原始年份回退未完成；曲目艺人多值、标签署名（含 tag 来源）、合辑标志已直接接入。APEv2 专辑艺人问题由测试失败实证，其余影响由调用链确认。
- **规则组合风险**：文件名推断的 `VA` 等符合现有整串占位判定，会成为高置信度合辑决定；曲目层同名艺人仍保留。未擅自增加来源限制；普通文件名艺人与标签艺人复用既定拆分规则，未发现新增误拆路径。
- **物理轨通路无丢失**：已正确解析的署名 role/name/detail/source 与作品字段随完整 observation 暂存、碟目录合并、format_split，再按曲目持久化。公开 REST 返回 detail，不返回 source，是现行合同。
- **既有边界**：CUE 虚拟观察未继承父文件 Credits/Compilation，在合并前 T 线已存在；不能把物理轨结论扩展为所有 CUE 虚拟轨。只列风险，不改继承规则。

## 实际验证

所有命令均在本 worktree；除前端与 Git 检查外，工作目录为 `backend/`。

| 命令 | 结果 |
| --- | --- |
| `gofmt -l .` | 通过，无输出 |
| `go build ./...` | 最终通过；首次与前端清空重建 assets 并发，embed 旧文件列表报不存在；前端构建完成后重跑通过，没有改代码规避 |
| `go vet ./internal/...` | 通过 |
| `env -u ROOMUSIC_TEST_DATABASE_URL go test ./internal/library/... ./internal/catalog/... ./internal/platform/... -count=1` | **失败**：仅 parser 的 `TestAPEv2Tags` 四个版本/有无头子用例失败，`lossless_test.go:195` 显示原始 `ALBUM ARTIST` 证据存在但 `AlbumArtist` 为空；其余包通过。需要数据库的测试跳过，迁移加载未出现编号不连续错误 |
| `env -u ROOMUSIC_TEST_DATABASE_URL go test ./internal/app -run '^TestPackageDependencyDirections$' -count=1` | 通过 |
| `git diff --check`、`git diff --cached --check` | 通过 |
| `cd frontend && npm run typecheck && npx vitest run src/features/catalog src/features/search src/test/api-contract.test.ts` | 通过：21 文件，916 通过、1 跳过 |
| `cd frontend && npm run build` | 通过；PostCSS 插件未传 `from` 的警告，不阻断构建 |
| `rg --hidden`（完整命令见下） | 旧迁移名零匹配，包含隐藏的 `.trellis/` 文档 |
| `git ls-files -u` | 无输出，四处索引冲突均已解除 |

旧名检索命令（零匹配的退出码为 1）：

```bash
rg --hidden '003[456]_(credit_roles|classical_works|search_punctuation)' .
```

另用 Python 静态核对迁移序列、上下行配对、SQL 重命名前后字节相等，以及回滚夹具版本完整。

未运行全量测试：按用户要求只运行指定相关范围；数据库集成测试由后续验收 agent 执行。首次验证因上述 APEv2 失败，**当时不能宣称全部门禁通过或已可直接合入**；后续裁决与定向复验见下节。

## 裁决后的最小接线修复

用户于 2026-11-08 批准将漏掉的收尾调用作为合并语义冲突修复：`parser/ape.go`、`wavpack.go`、`aiff.go` 各增加一行 `finalizeTags(&observation)`，位置为全部标签读取完成后、`applyFilenameFallback` 之前。无重复实现、无新抽象、无测试断言调整。

APEv2 已把文本键逐个交给 `setAudioTag`，同名 `WORK/MOVEMENTNAME/MOVEMENTNUMBER/MOVEMENT`、⑦署名键及演奏乐器括号直接走 Vorbis 共用入口，无缺失映射需要补丁。ID3 的 TIPL/TMCL 成对帧不是 APEv2/Vorbis 的同名文本键，本次未新增这种语法。

文件名 `VA` 等成为合辑决定已获接受，不改规则。CUE 虚拟轨不继承父音频 Credits/Compilation 保留，并在扫描合同 CUE 对应位置记为“已知边界”。扫描合同同时明确所有新格式的标签收尾顺序，防止后续新增容器只接收集入口、漏掉延迟赋值。

### M 线⑩ APE fixture 观察值

- 固定二进制 `backend/internal/playback/derivation/testdata/ape-16-44100.ape` 未修改；实际读取大小 3468 字节、SHA-256 为 `877679256060ae2bfb2eec861bd3125ebd3591596e7582b5d1b761b75a971475`，与 M 线记录一致。检查文件尾无 ID3v1 或 APEv2 页脚，素材没有这次需要最终处理的标签。
- 按素材和收尾代码核对：标题文件名兜底、未知艺人、目录专辑兜底、专辑艺人/作品/歌词为空、年份为 0 等业务观察值不变；音频参数仍为 APE、16 位、44100 Hz、双声道、0.25 秒，指纹算法与文件均未改。没有额外执行观察探针或测试命令。
- 内存结构有一处规范化差异：`CanonicalCredits(nil)` 返回非 nil 的空切片，所以固定素材的 `Credits` 从 nil 变为空切片；无署名事实新增。这不是业务标签变化。
- `TestAPEv2Tags` 的合成带标签 APE/WV 观察确实变化：`AlbumArtist` 从空值恢复为“专辑艺人”，两项 composer 经统一规则排序；测试原断言不改。该夹具没有作品/乐章或歌词头部署名，不能将其通过当作这些字段的文件级组合验证。

### 本轮验证范围

只执行用户指定的四项门禁，不跑全量、不启动数据库、不重建前端；未提交。检查代理分别执行，各命令退出码为 0：

| 目录 | 命令 | 结果 |
| --- | --- | --- |
| `backend/` | `gofmt -l .` | 无输出，通过 |
| `backend/` | `go vet ./internal/library/...` | 无输出，通过 |
| `backend/` | `env -u ROOMUSIC_TEST_DATABASE_URL go test ./internal/library/... -count=1` | library、httpapi、parser 通过；parsertest 无测试文件 |
| 仓库根 | `git diff --check` | 无输出，通过；记录结果后再次检查仍通过 |

`TestAPEv2Tags` 的四个版本/有无头子用例，以及 `TestAPERealFixture` 均包含在本次无筛选、禁用缓存的 parser 包执行中，未跳过；没有修改它们的断言。固定 APE 的音频参数与内容指纹断言通过。数据库用例因未提供连接而跳过，不作为集成验收证据。
