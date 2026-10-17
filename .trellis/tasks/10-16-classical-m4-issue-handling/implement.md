# M4 问题处理：执行计划

需求见 [prd.md](prd.md)，设计见 [design.md](design.md)。

**通用约定**

- 提交：每一步单独提交到本地 `main`，提交信息用中文，不推送，不用 `--amend`。
  改动前端源码的提交要同时重新构建并提交 `backend/cmd/roomusic/web`。
- 提交前检查：用 `git checkout-index -a --prefix=<临时目录>/` 导出暂存区，在完整文件树上跑这一步的验证；`git diff --cached --check` 必须干净
  （新建的文件要在暂存之后检查）。
- 数据库测试：只用 `roomusic-test` Compose 项目（`--env-file /dev/null`、绝对路径的 compose 文件、空闲端口、随机库名，结束时 `down -v`），
  不碰用户自己的 `roomusic` 项目；确认集成测试是真跑了（PASS，不是 SKIP）。
- 素材：自动化只用合成素材，不读真实 `music/`。
- 停进程：只按 PID 停自己启动的进程。

## 步骤

- [x] **1. 迁移 0019 与“需要关注”的三类拆分（无行为变化）**
  - 迁移 0019（design §3）；`operations.TypeReleaseIssueReview`。
  - `releaseAttentionExpression` 拆成三个具名常量再相加，加入核对的排除；另加三个“已核对”常量（design §4）。
  - 门禁：没有核对行时既有集成测试与接口样本不变。新测试：手工写入核对行后三类各自减少，组的计数与 `attention=required` 一致。
- [x] **2. 核对的写入与撤销**
  - `catalog/issue_review.go`：依据 SQL、令牌、请求解析与指纹、执行器、`Kind`（撤销、展示）；`catalog/httpapi` 的写接口；`app.go` 注册（design §5、§6）。
  - 集成测试：四类核对各自消除计数；`issue_changed`（令牌不符、问题已不存在）、`no_changes`、`revision_conflict`、`not_found`、
    同源与权限、同键重放与同键异义；过期判断（来源路径变了、候选变了、曲目回来又缺失、新缺失的曲目只计新的）；
    取消核对（含过期的行）；撤销（恢复前态与原核对人、过期也恢复）、`revert_conflict`；记录列表的改动摘要与 `revertible`；
    核对与修正、扫描并发不死锁。接口样本增加写入、撤销与记录项。
- [x] **3. 问题的读取**
  - `GET /api/v1/release-issues`（含 `summary`）与 `GET /api/v1/releases/{id}/issues`（design §7）。
  - 集成测试：各类数目之和等于 `attention_count`（列表、详情、组）；状态与类型筛选、分页、非法筛选；只有管理员；同目录的其他版本；
    缺失曲目的上限；两次读取之间令牌不变。
  - 真实重扫（合成 FLAC）：同一目录两种专辑标签造出归组冲突，`MEDIA` 标签不一致，删除文件造出缺失；核对后原样重扫保持，
    改动之后重新计入；扫描不改核对行与记录数。
  - 接口样本增加两个读取接口。
- [x] **4. 前端：问题页（只读）**
  - 解码器、端点、查询键、`issues-search.ts`；`IssuesView`（列表、详情、两栏与顺序导航）；路由 `/issues`（`AdminGate`）；导航项（design §8）。
  - Vitest：筛选与数字、选中写进地址、窄容器顺序导航、空态与错误态、普通成员没有入口。
- [ ] **5. 前端：核对、取消核对与撤销**
  - 命令意图与结果提示；从详情打开“编辑发行信息”；`issue_changed` 后重新读取；操作记录与“修改记录”的文案；专辑详情的“查看问题”。
  - Vitest：每类的核对与取消、撤销、冲突与结果不确定时的重试、提交后各处计数一起变化。
- [ ] **6. Playwright（受控后端）**：主流程、五种宽度不溢出、axe（WCAG 2.1 A/AA）、纯键盘；`src/test/governance-data.ts` 增加问题与核对的接口行为，
  Vitest 替身与受控后端共用。
- [ ] **7. 真实服务用例**：`frontend/e2e/real/issues.real.ts`，用合成 WAV 造出归组冲突与缺失曲目，在页面上核对、重扫、取消与撤销；用例结束删除素材并重扫。
- [ ] **8. 文档同步**：新增后端规范 `issue-review-contracts.md`；同步受治理操作合同与 catalog 合同的“需要关注”、前端规范、
  DESIGN 三份（布局 token、页面能力矩阵、落地差异）、ADR 0002/0003 的决策记录、路线图（M4 第二项）、父任务 PRD 与 README。
- [ ] **9. 收尾**：布局截图请用户确认（问题页宽屏与窄屏、详情三节、已核对与过期、操作记录），PRD 实施结果与验收对照，
  归档（`--skip-branch-validation`），相对链接的修正单独提交，写 journal，更新记忆。

## 验证命令（每步取最小集合）

- **环境准备**：`/tmp` 重启后会清空。隔离数据库的启停脚本、Playwright 需要的 Chromium 系统库（解包到 `/tmp/chromelibs`，
  用 `LD_LIBRARY_PATH` 指过去，不装系统包）与链接检查脚本，缺了就重新准备；ffmpeg 缓存在 `$HOME/.cache/roomusic/`。
- **后端**：`gofmt -l .`、`go vet ./...`。在隔离 PostgreSQL 上跑：
  - `go test -race -count=1 ./internal/operations/...`
  - `go test -count=1 ./internal/catalog/... ./internal/app/... ./cmd/...`
  - 真实重扫相关的用例另跑 `go test -count=1 ./internal/library/...`。
- **前端**：
  - 每步：`npm run lint`、`npx tsc --noEmit`、`npx vitest run <相关目录>`；
  - 提交前：全量 Vitest、`npm run build` 并核对内嵌产物、`npm run test:e2e`（跑完 `git status` 必须干净）。
- **真实服务**：先拆掉自己的测试库，再跑 `./scripts/playback-e2e.sh`。
- **文档**：相对链接检查、规范文件不超过 24,000 字节、`python3 ./.trellis/scripts/task.py validate`。

## 风险与回滚点

- **第 1 步改动所有列表与详情的“需要关注”**：门禁是没有核对时输出逐项不变，失败就回退这一步。
- **扫描写入路径不改**：实现中如果发现必须改 `persist*.go`，先停下说明原因，不扩大范围。
- **依据令牌依赖 JSONB 文本输出稳定**：第 3 步有测试；不稳定时改为在 Go 里按字段规范化之后再取摘要，接口不变。
- **缺失曲目的时间比较**：依据用微秒整数，写入与比较用同一个表达式；测试覆盖“同一时间相等、曲目再次出现后不等”。
- **规范文件接近上限**：状态规范 23.7 KB、运行合同 23.8 KB、扫描合同 23.3 KB、组件规范 22.6 KB 都不再加内容；
  合同写进新文件 `issue-review-contracts.md`，前端细节放进有余量的钩子规范与目录规范。
- 每一步独立提交，可以单独 revert；迁移 0019 一旦提交，就只向前修复。

## 启动前

- 规划文档先单独提交，再运行 `python3 ./.trellis/scripts/task.py start`。
