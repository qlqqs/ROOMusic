# M4 认领：执行计划

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
- 每个修正都要配一个回归测试，并做变异检查：临时去掉修正，确认新测试会失败，再还原。

## 步骤

- [x] **1. 迁移 0020 与待认领的判定（只读，无行为变化）**
  - 迁移 0020：放宽 `operations_type_ck`，建 `operations_claim_source_index`（design §4）；`operations.TypeReleaseClaim`。
  - `catalog/claims.go`：`releaseClaimablePredicate`、路径集合与推荐的 SQL（design §2、§3）。
  - 门禁：既有集成测试与全部接口样本逐字不变。
  - 新集成测试：
    - 四类人工状态各自让不可见的版本成为待认领；可见的、或没有人工状态的不是；
    - “组里有别的发行”包括不可见的成员；
    - 推荐：用 `writeSyntheticTrack`/`runScan` 造出原地替换、新增文件、目录不变而文件整体换了名字与内容三种情形（单纯改名不新建发行，不属于认领），前两种命中 `same_files`，第三种只命中 `same_directory`；
      最多 5 个，排序符合规则。
- [x] **2. 认领的预览与写入**
  - `catalog/claim.go`：预览（`items` 与令牌）、请求解析与指纹、`ClaimExecutor`；`catalog/httpapi` 的预览与写接口（design §5、§6）。
  - 集成测试：
    - 四类各自迁移；K3 的每种跳过（字段已有修正、问题已有核对、已在多版本专辑里、已在 G 里）；默认版本的三种结果（跟着进入、改回自动选择、旧版本是别的组的默认版本）；
      缺失曲目的核对不迁移；
    - 认领之后旧版本不再是待认领，移进了新建的空组；四类全部跳过时也是如此；
    - 迁过去的核对在新版本依据相同时有效、不同时重新计入“需要关注”；
    - 拒绝且没有副作用：`not_claimable`（可见的、没有人工状态的）、`claim_changed`（令牌不符）、`revision_conflict`（两个版本号各自过期）、
      `404`、`400 invalid_id` 与 `invalid_input`（同一个版本、缺键）、跨源与非管理员 403、未登录 401；
    - 同键重放与同键异义；与修正、归组、默认版本、核对、扫描随机交错并发不死锁（含“认领迁移核对”与“撤销那条核对的原记录”交叉，去掉 KEY SHARE 锁时应报 `40P01`）。
  - 接口样本：预览与写入。
- [ ] **3. 撤销、展示与操作记录**
  - `ClaimOperations()`（撤销、展示），`app.go` 注册；`operations.ListParams.Sources` 与“修改记录”的并集（design §7）。
  - 集成测试：撤销逐项恢复两个版本与各组的前态（修正与核对的原 `operation_id`、组、默认版本）；新版本之后又修改过、旧版本或组的版本号变了时 `revert_conflict`；
    新版本变得不可见时仍可撤销；撤销不能再撤销；`Describe` 的改动摘要与 `revertible`；按版本筛选同时列出以它为来源的认领与撤销记录。
  - 接口样本：记录列表里的认领项与撤销项。
- [ ] **4. 待认领的读取**
  - `GET /api/v1/release-claims`、`GET /api/v1/release-claims/{id}`、`GET /api/v1/releases/{id}/claim-hints`（design §8）。
  - 集成测试：只有管理员；排序（最后一次看到的时间）与分页；`root_name` 与目录不含绝对路径；`album` 的标题取法；详情的修改明细与推荐；
    `claim-hints` 与详情的推荐一致；认领之后从列表消失、旧版本可见之后不再列出。
  - 真实重扫（合成 FLAC）：修正、归组、设默认版本、核对之后整张原地替换并重扫，旧版本出现在列表、推荐命中新版本；认领之后再原样重扫，四类都保持；扫描不改操作记录。
  - 接口样本：三个读取接口。
- [ ] **5. 前端：待认领页（只读）**
  - 解码器、端点、查询键、`claims-search.ts`；`ClaimsView`（列表、详情、两栏与顺序导航）；路由 `/claims`（`AdminGate`）；导航项（design §9）。
  - Vitest：列表与详情、推荐与理由文字、选中写进地址、窄容器顺序导航、空态与错误态、普通成员没有入口且不请求接口。
- [ ] **6. 前端：选择、预览、认领与撤销**
  - 推荐与搜索的单选（搜索先选专辑再选版本）、预览列表、命令的第七种意图 `claim`、结果提示与撤销；专辑详情的“可能接替了已不可见的《…》”；
    操作记录与“修改记录”的文案。
  - Vitest：每类的迁移与跳过的预览文字、`claim_changed` 与 `revision_conflict` 后重新读取、结果不确定时用同一个键重试、提交后焦点落在结果提示上、
    撤销后页面回到原来的专辑、提交后列表与专辑数据一起刷新。
- [ ] **7. Playwright（受控后端）**：主流程、五种宽度不溢出、axe（WCAG 2.1 A/AA）、纯键盘；`src/test/governance-data.ts` 增加认领的接口行为，Vitest 替身与受控后端共用。
- [ ] **8. 真实服务用例**：`frontend/e2e/real/claims.real.ts`：用合成 WAV 建两版专辑，在页面上修正、归组、设默认版本、核对；整张替换其中一版并重扫；
  在“待认领”里按推荐认领，检查专辑页；撤销后再认领；最后删除素材并重扫。先用一个临时 Go 测试（`writeSyntheticTrack`/`runScan`）验证这个扫描情形，再跑慢的 `playback-e2e.sh`。
- [ ] **9. 文档同步**：新增后端规范 `claim-contracts.md`；同步受治理操作合同（第 7 节的 G8 限制改为指向认领）、核对合同第 7 节、Catalog REST 合同、数据库、目录结构、
  运行合同与错误处理、前端规范、DESIGN 三份（布局、页面能力、落地差异）、ADR 0002/0003 的决策记录、路线图（M4 第三项）、架构决策摘要、产品目标、README 与父任务 PRD；
  规范文件都不超过 24,000 字节。
- [ ] **10. 收尾**：布局截图请用户确认（待认领页宽屏与窄屏、推荐与搜索、预览、结果提示、专辑详情的提示、操作记录），PRD 实施结果与验收对照，
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

## 风险与回退点

- **第 1 步**改的只是只读判定与迁移；检查标准是既有接口样本逐字不变，不一致就回退这一步。
- **第 2、3 步**是唯一同时写两个发行与多个组的操作。加锁顺序（KEY SHARE → 发行 → 组）是死锁防线，必须有交叉并发的回归测试。
- **撤销不要求可见**是对共同规则的例外，只在认领的 `Kind` 里实现，不改 `operations` 与其他类型。
- **扫描写入路径不改**。实现中如果发现必须改扫描，先停下来与用户说明。
- 整体回退：关闭认领的写入路由与页面入口，保留数据与记录（design §12）。
