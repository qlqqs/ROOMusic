# Journal - qlqq (Part 1)

> AI development session journal
> Started: 2026-08-31

---



## Session 1: Bootstrap ROOMusic Core 0 development guidelines
<!-- trellis-session: v=2 fp=b4f507a4f2a5db2e -->

**Date**: 2026-09-01
**Task**: Bootstrap ROOMusic Core 0 development guidelines
**Branch**: `main`

### Summary

Replaced generated Trellis backend/frontend templates with ROOMusic Core 0 contracts, added product/modular/engineering guides, synchronized indexes, and verified links, placeholders, boundaries, and whitespace. Left Core 0 planning and architecture canvas changes untouched.

### Git Commits

| Hash | Message |
|------|---------|
| `94b3e3f` | docs(trellis): bootstrap ROOMusic Core 0 guidelines |

### Status

[OK] **Completed**


## Session 2: 完成 Core 0 首个可浏览纵向切片
<!-- trellis-session: v=2 fp=cf67bde2ddece90f -->

**Date**: 2026-09-01
**Task**: 完成 Core 0 首个可浏览纵向切片
**Branch**: `main`

### Summary

完成 Go/PostgreSQL 后端、React 前端、身份会话、受限目录、FLAC/MP3 扫描、最小 Release Graph 浏览闭环；修复 setup 加载错误展示并更新工具规范。通过 Go test/vet/build、前端 lint/typecheck/test/build 与 docker compose config。

### Git Commits

| Hash | Message |
|------|---------|
| `4111d69` | fix(core): harden browse slice loading and docs |

### Status

[OK] **Completed**


## Session 3: 完成扫描格式与 CUE 扩展
<!-- trellis-session: v=2 fp=90dc35588615d7e4 -->

**Date**: 2026-09-01
**Task**: 完成扫描格式与 CUE 扩展
**Branch**: `main`

### Summary

增加 OGG、Opus、WAV 基础解析与受限 UTF-8/UTF-16 CUE 虚拟 Track 扫描，强化 FILE/TRACK/INDEX、codec header、路径 containment 和稳定来源身份校验；通过 Go test/vet/build、前端 lint/typecheck/test/build、docker compose config。

### Git Commits

| Hash | Message |
|------|---------|
| `edf7120` | feat(core): expand audio formats and cue scanning |

### Status

[OK] **Completed**


## Session 4: 完成 PostgreSQL 基础搜索
<!-- trellis-session: v=2 fp=ed48783a4f4bc3f6 -->

**Date**: 2026-09-01
**Task**: 完成 PostgreSQL 基础搜索
**Branch**: `main`

### Summary

为 Release 列表增加参数化 PostgreSQL 搜索，覆盖 Release 标题、艺术家和 Track 标题；加入分页一致性、通配符转义、URL 搜索状态及前端 loading/empty/error/retry。通过 Go 与前端质量门禁。

### Git Commits

| Hash | Message |
|------|---------|
| `5ea3141` | feat(core): add PostgreSQL release search |

### Status

[OK] **Completed**


## Session 5: 完成 Release 封面体验
<!-- trellis-session: v=2 fp=bdf90a9341598c1a -->

**Date**: 2026-09-01
**Task**: 完成 Release 封面体验
**Branch**: `main`

### Summary

增加 release-level 封面迁移、内嵌/目录发现、hash 幂等 data 存储、鉴权资源 API 和前端详情展示；封面失败隔离于音频扫描。通过 Go、前端、Compose 与 diff 质量门禁。

### Git Commits

| Hash | Message |
|------|---------|
| `26d4ffb` | feat(core): add release artwork storage |

### Status

[OK] **Completed**


## Session 6: 完成 Core 0 私有多用户管理
<!-- trellis-session: v=2 fp=1d7b071c1da28657 -->

**Date**: 2026-09-01
**Task**: 完成 Core 0 私有多用户管理
**Branch**: `main`

### Summary

新增 admin/user 角色、用户创建与禁用、会话撤销和管理员权限矩阵；禁用用户即时失效，前端按角色隐藏目录管理控件。通过 backend go test/vet、frontend lint/typecheck/test 与 git diff --check。

### Git Commits

| Hash | Message |
|------|---------|
| `bc0ead7` | feat(core): add private multi-user management |

### Status

[OK] **Completed**


## Session 7: 完成 Core 0 目录操作治理
<!-- trellis-session: v=2 fp=e26d20bfd278353a -->

**Date**: 2026-09-01
**Task**: 完成 Core 0 目录操作治理
**Branch**: `main`

### Summary

为 library root 增加 active/disabled 生命周期、revision、幂等键和操作日志，新增停用/恢复/历史 API，扫描过滤停用目录，前端展示状态与操作控件。通过 backend go test/vet、frontend lint/typecheck/test 和 git diff --check。

### Git Commits

| Hash | Message |
|------|---------|
| `a57c212` | feat(core): govern library root operations |

### Status

[OK] **Completed**


## Session 8: 完成 Core 0 最终集成验收
<!-- trellis-session: v=2 fp=e4aca6aa54f184f7 -->

**Date**: 2026-09-01
**Task**: 完成 Core 0 最终集成验收
**Branch**: `main`

### Summary

补齐真实 PostgreSQL 权限矩阵和目录事务集成测试，修复全新迁移重复列、JSONB 参数绑定及操作错误分类问题；复验后端、前端、生产构建和 Compose 门禁，归档补测子任务与 Core 0 父任务。

### Git Commits

| Hash | Message |
|------|---------|
| `1013177` | chore(core): refresh embedded frontend assets |
| `ca4671a` | test(core): verify authorization and root transactions |

### Status

[OK] **Completed**


## Session 9: 完成 Core 0.1 反代与开发工作流
<!-- trellis-session: v=2 fp=db9b53d4321dde7f -->

**Date**: 2026-09-01
**Task**: 完成 Core 0.1 反代与开发工作流
**Branch**: `main`

### Summary

新增 ROOMUSIC_PUBLIC_URL 严格来源配置、开发数据库重置脚本、一键 dev 热更新工作流，并补齐前端用户管理与目录操作历史界面；通过后端、前端、构建和 Compose 门禁。

### Git Commits

| Hash | Message |
|------|---------|
| `ffed522` | feat(core-0.1): add reverse proxy config and dev workflow |
| `9d30566` | chore(core-0.1): refresh embedded frontend assets |

### Status

[OK] **Completed**


## Session 10: Core 0 运行契约修复与规范刷新
<!-- trellis-session: v=2 fp=18b137fc36871dd1 -->

**Date**: 2026-09-01
**Task**: Core 0 运行契约修复与规范刷新
**Branch**: `main`

### Summary

修复生产启动脚本路径、前端响应解码与焦点可访问性、用户启停事务和最后管理员保护、扫描不支持格式与多碟媒体归属；刷新 Trellis 前后端及架构规范，登记迁移执行器与持久化扫描取消为后续技术债。已通过前后端质量门禁、Compose、脚本和 Trellis 校验。

### Git Commits

| Hash | Message |
|------|---------|
| `a15487f` | fix(core): harden runtime contracts and refresh trellis specs |

### Status

[OK] **Completed**


## Session 11: PostgreSQL 集成测试基础设施
<!-- trellis-session: v=2 fp=e6b3437e28ff653c -->

**Date**: 2026-09-02
**Task**: PostgreSQL 集成测试基础设施
**Branch**: `main`

### Summary

新增 PostgreSQL-only Compose 测试环境与 test-integration 脚本，补充真实数据库回归用例，更新 README、Trellis 质量规范和 mise 任务。修正目录操作 SQL 类型和未知用户测试断言。集成门禁及 Go 质量检查通过。

### Git Commits

| Hash | Message |
|------|---------|
| `eaf9430` | test(core): add postgres integration test foundation |

### Status

[OK] **Completed**


## Session 12: PostgreSQL 迁移执行器治理
<!-- trellis-session: v=2 fp=3e2cbff03900622f -->

**Date**: 2026-09-02
**Task**: PostgreSQL 迁移执行器治理
**Branch**: `task/postgres-migration-governance`

### Summary

完成迁移执行器治理：加入连续版本发现、原始字节 SHA-256、事务级 advisory lock、旧 1/6/7 历史基线、漂移与未知版本 fail-closed、提交前 tracking 复核及 PostgreSQL 18 集成测试；同步更新数据库规范、运行合同和运维文档。

### Git Commits

| Hash | Message |
|------|---------|
| `495bb7e` | feat(db): govern PostgreSQL migration execution |

### Status

[OK] **Completed**


## Session 13: 记录真实音乐资产长期记忆
<!-- trellis-session: v=2 fp=49e91fe9b5eacbe2 -->

**Date**: 2026-09-02
**Task**: 记录真实音乐资产长期记忆
**Branch**: `main`

### Summary

将根目录 music/ 是真实音乐资产这一事实写入 Trellis 长期规范，明确只读、测试与 CI 隔离及信息披露边界；完成质量检查并归档小任务。

### Main Changes

- 新增 .trellis/spec/guides/real-music-assets.md 并加入共享指南索引。

### Git Commits

| Hash | Message |
|------|---------|
| `269c17c` | docs(trellis): record real music assets |

### Testing

- [OK] Markdown 相对链接 12 项通过，git diff --check 通过，确认 music/ 无 Git 状态变化。

### Status

[OK] **Completed**

### Next Steps

- 继续处理已有的 CI 门禁与 HTTP 可观测性任务。


## Session 14: CI 门禁与 HTTP 可观测性
<!-- trellis-session: v=2 fp=38535c870fa225c5 -->

**Date**: 2026-09-02
**Task**: CI 门禁与 HTTP 可观测性
**Branch**: `main`

### Summary

完成 GitHub Actions 后端、前端和 PostgreSQL 18 门禁；将 HTTP 请求日志改为完成事件，记录状态、字节、耗时、路由模板、request_id 和可选 actor_id；补充脱敏测试、README 与运行规范。Go、race、vet、build、前端检查、Compose、Shell 和 PostgreSQL 18 集成测试均通过。

### Git Commits

| Hash | Message |
|------|---------|
| `25d521f` | feat: add CI gates and HTTP observability |

### Status

[OK] **Completed**


## Session 15: 持久化扫描取消与跨进程协调
<!-- trellis-session: v=2 fp=a74b3e7c91500c2f -->

**Date**: 2026-09-02
**Task**: 持久化扫描取消与跨进程协调
**Branch**: `main`

### Summary

完成 PostgreSQL 持久化扫描取消、session advisory lock 跨进程单任务协调、锁感知恢复、终态与缺失来源原子对账、管理端状态恢复与取消交互；补齐七段式运行合同并消除过时描述。后端、前端、race、构建与 PostgreSQL 集成检查已在任务质量阶段通过，本次规范修正通过任务上下文校验和 git diff 检查。

### Git Commits

| Hash | Message |
|------|---------|
| `511cea3` | feat: add persistent scan cancellation coordination |
| `579fa45` | docs: clarify persistent scan coordination contract |

### Status

[OK] **Completed**


## Session 16: 完成专辑扫描解析与候选闭环
<!-- trellis-session: v=2 fp=6a49f289337171af -->

**Date**: 2026-09-03
**Task**: 完成专辑扫描解析与候选闭环
**Branch**: `feat/album-scan-organization-core`

### Summary

完成有界 M4A/CUE 解析、确定性专辑归组、staging 与候选持久化、REST/前端只读证据闭环，并通过 PostgreSQL 18 与全量质量门禁。

### Git Commits

| Hash | Message |
|------|---------|
| `a527d2c` | feat: repair bounded audio and cue parsing |
| `8ccc269` | feat: persist deterministic album scan candidates |
| `19e2a8e` | feat: expose album scan evidence |
| `851351e` | feat: add read-only album evidence views |
| `02bb425` | docs: record album scan contracts |

### Status

[OK] **Completed**


## Session 17: 完成真实音乐库只读 Smoke 与 V0 对照
<!-- trellis-session: v=2 fp=6a9b62ff4b929d8e -->

**Date**: 2026-09-04
**Task**: 完成真实音乐库只读 Smoke 与 V0 对照
**Branch**: `task/real-library-smoke-v0-comparison`

### Summary

建立固定 V0 scanner 的隔离 SQLite/canonical 基准，完成 399 个真实资产的 current 首扫与重扫，对照收口扫描、元数据、艺人署名和严格多碟差异；最终 current A/B、current regression 与未知分类均为 0，资产前后摘要一致。

### Git Commits

| Hash | Message |
|------|---------|
| `efd5317` | feat: add isolated real-library smoke tooling |
| `2747989` | fix: close V0 comparison regressions |
| `b718d2e` | docs: record real-library smoke verification |

### Status

[OK] **Completed**


## Session 18: 迁移 ROOMusic 开发环境到 qlqq
<!-- trellis-session: v=2 fp=8702d5ac23d90673 -->

**Date**: 2026-09-04
**Task**: 迁移 ROOMusic 开发环境到 qlqq
**Branch**: `task/real-library-smoke-v0-comparison`

### Summary

完成 ROOMusic 开发环境、工具链和 Paseo 服务从 root 到 qlqq 的可回滚迁移；qlqq Paseo 已接管 6767，最终按用户要求以表面检查收口并记录已知版本与额外工作区偏差。

### Main Changes

- 归档迁移规划、设计、执行证据和中文 migration-report。
- 确认 qlqq Paseo enabled/active、root Paseo disabled/inactive，工作区与进程均为非 root。

### Git Commits

| Hash | Message |
|------|---------|
| `a427377` | docs: record qlqq environment migration |

### Testing

- [OK] 按用户要求未重跑 lint、typecheck、构建、测试和 Agent smoke；完成 systemd、HTTP、进程 UID、Docker、路径、权限、Git 与 Trellis 文档轻量检查。

### Status

[OK] **Completed**

### Next Steps

- 无；保留 root 环境和权限受限快照作为观察期回滚资料。


## Session 19: 完成发行列表后端封面摘要
<!-- trellis-session: v=2 fp=21ea6dca165ffa28 -->

**Date**: 2026-09-04
**Task**: 完成发行列表后端封面摘要
**Branch**: `task/release-list-artwork-backend`

### Summary

发行列表与详情现共享一次 LEFT JOIN 的 nullable artwork 摘要投影，非法元数据安全失败；补齐单元、PostgreSQL 18 集成测试和 Catalog REST 合同。后端子任务已归档，父级前端任务继续保持规划状态。

### Git Commits

| Hash | Message |
|------|---------|
| `52c5850` | feat: expose artwork in release summaries |
| `9315a0b` | docs: record staged music display task context |

### Status

[OK] **Completed**


## Session 20: 完善音乐库前端展示
<!-- trellis-session: v=2 fp=664c3e134965736c -->

**Date**: 2026-09-04
**Task**: 完善音乐库前端展示
**Branch**: `task/music-display-frontend`

### Summary

实施并验收前端子任务 09-04-music-display-frontend：catalog feature 分层（model/纯 formatter + components）、列表封面四态与严格 artwork decoder、URL release 参数与详情抽屉焦点/生命周期、本地演示队列、响应式与可访问性；lint/typecheck/test/build 与后端定向回归通过；修复抽屉 aria-labelledby 悬空；spec 记录 catalog 分层与 artwork 消费合同。

### Git Commits

| Hash | Message |
|------|---------|
| `b85122e` | feat: build catalog browsing workspace frontend |
| `663c9bd` | chore: rebuild embedded web assets |
| `d0afb98` | docs: record catalog feature layering and artwork consumption |

### Status

[OK] **Completed**


## Session 21: 收尾前端音乐库展示父任务
<!-- trellis-session: v=2 fp=1a116be0f2a78ca3 -->

**Date**: 2026-09-04
**Task**: 收尾前端音乐库展示父任务
**Branch**: `main`

### Summary

核对并归档已完成的音乐库展示父任务；最终复核统一 Release URL 选择值与 artwork resource_id 的 255 边界，补充回归测试和代码规范，重建内嵌前端资产。

### Git Commits

| Hash | Message |
|------|---------|
| `f8151e0` | fix: bound catalog resource identifiers |
| `abab775` | chore: rebuild embedded web assets |

### Status

[OK] **Completed**


## Session 22: 修复扫描元数据与前端标签显示
<!-- trellis-session: v=2 fp=d17e146708fa8b78 -->

**Date**: 2026-09-04
**Task**: 修复扫描元数据与前端标签显示
**Branch**: `main`

### Summary

修正开发音乐根目录并完成只读重扫，恢复厂牌、条码、目录号与署名；统一来源/介质显示去重，完成月刊式前端改版、设计锚和嵌入资源提交。

### Git Commits

| Hash | Message |
|------|---------|
| `f2615d5` | feat: refine catalog presentation and metadata display |

### Status

[OK] **Completed**


## Session 23: Classical M0 文档对齐与提交收尾
<!-- trellis-session: v=2 fp=1a18c58f4406fc56 -->

**Date**: 2026-09-27
**任务**: Classical M0 文档对齐与提交收尾
**分支**: `docs/classical-m0-alignment`

### 摘要

完成 Classical 设计依据、Core 1 能力路线、四份 ADR 草案与规范对齐。用户明确批准提交后创建文档分支并完成工作提交，随后仅归档 M0、修复父子任务引用。父任务仍在规划，ADR 与 Core 1 截止范围仍待批准，未推送。

### 主要变更

- 新增 frontend/DESIGN.md、docs/architecture/core-1-roadmap.md 与四份未采纳 ADR；同步 README、架构摘要和相关前后端规范。
- 工作提交 850d274 只含 36 个文档/任务文件；M0 已归档至 .trellis/tasks/archive/2026-09/09-27-classical-m0-alignment，父任务保持 planning，旧任务未修改或归档。
- 归档时修复 Markdown 链接、JSONL 上下文和 relatedFiles；保留工作提交清单与归档前实际执行命令，不改写历史。
- 目录幂等指纹缺少路由目标 ID 等风险仅作静态记录，未动态复现或修复；未启动 M1/B0，未安装依赖或修改业务、原型与媒体。
- 验证边界为纯文档：未运行 Go/npm/数据库全量测试、UI 或真实媒体测试，不声称浏览器兼容、播放性能或发行许可已验收。

### Git 提交

| 提交哈希 | 说明 |
|------|---------|
| `850d274` | docs: 对齐 Classical 设计与 Core 1 路线 |

### 验证

- [OK] python3 .trellis/scripts/task.py validate .trellis/tasks/09-27-classical-core-1；python3 .trellis/scripts/task.py validate .trellis/tasks/archive/2026-09/09-27-classical-m0-alignment：父子上下文均通过。
- [OK] python3 /tmp/roomusic-m0-review-check.py；python3 /tmp/roomusic-m0-validate.py：归档前执行，36 个文件、177 个本地链接与原型 token、静态对比度和扫描字段通过。
- [OK] python3 /tmp/roomusic-m0-archive-check.py：归档后 36 个文件、178 个本地链接、元数据和父子关系通过；临时检查器不是仓库工具。
- [OK] git diff --check；git diff --cached --check；受保护路径 git diff --exit-code：均通过，无业务、原型、依赖或旧任务变更。

### 状态

[OK] **已完成**

### 后续步骤

- 由用户审阅四份 ADR 草案和 Core 1 截止范围；明确决策后再独立创建、规划并批准 M1/B0 等任务，不自动开始业务实施。


## Session 24: M2a 播放器续接与自动化验收
<!-- trellis-session: v=2 fp=9d60cf564e85c863 -->

**Date**: 2026-10-05
**Task**: M2a 播放器续接与自动化验收
**Branch**: `main`

### Summary

接续已批准的M2a第4–7步，补齐播放引擎、UI、隔离真实播放与合同；独立检查修复恢复、历史和无界文本边界。真实13项及前端938项通过；远端CI、三组手工实播与提交确认尚待，保持任务进行中。

### Main Changes

- 补齐播放器、公共入口、队列与按账号暂停恢复；既有Claude改动保留完善。
- 真实服务使用合成媒体、独立roomusic-test与临时构建树，受背压TCP验证撤销；新增CI作业。

### Git Commits

(No commits - planning session)

### Testing

- [OK] 前端 lint/typecheck、52文件938项Vitest通过；原生真实播放13项通过。
- [OK] 最小播放单元/隔离PostgreSQL播放与API样本集成race通过；未运行无关全量后端。

### Status

[OK] **Completed**

### Next Steps

- 全mock158项、播放器专项7项与最终375/1280截图已通过；119个内嵌文件重复构建字节一致，Go构建/资产合同通过。
- 用户确认四组提交；三组手工实播/远端CI结果完成后才归档。


## Session 25: M2a 审查修复与收尾归档
<!-- trellis-session: v=2 fp=5cc7b05daaaf1820 -->

**Date**: 2026-10-07
**Task**: M2a 审查修复与收尾归档
**Branch**: `main`

### Summary

复核 Codex 完成的 M2a 并修复 12 项问题；按获批计划分组提交第 4–7 步，ALAC 在 Safari 实播前移出直出格式表；三组手工实播、ALAC 恢复与远端 CI 移交 10-06-m2a-acceptance-followup，M2a 归档。

### Main Changes

- 修复审查问题：stalled 不算缓冲、加载中按钮文字、窄订阅与曲目行 memo、待定位位置、全局公告、当前曲目标记、随机后退、定时保存不重复序列化，以及文档与测试断言。
- 第 4–7 步各一个提交，每组先导出暂存区在完整文件树上验证；ALAC 移出格式表另成提交 13a9cfe，可 revert 恢复。
- 新建后续验收任务并移入手工验收表；M2a 归档并修正相对链接；未推送。

### Git Commits

| Hash | Message |
|------|---------|
| `d1c976d` | feat(player): 播放引擎、队列与能力判定 |
| `06071f8` | feat(player): 播放栏、正在播放页与专辑页播放入口 |
| `1096443` | test(playback): 真实服务播放自动化与 CI 作业 |
| `4dc1889` | docs(playback): 同步 M2a 合同、规范与路线 |
| `13a9cfe` | fix(playback): ALAC 在 Safari 实播确认前移出直出格式表 |
| `9cc11fd` | docs(m2a): 收尾决策、验收对照与遗留验收任务 |
| `564728a` | chore(task): archive 10-03-classical-m2a-direct-playback |
| `48b0d3f` | chore(task): 修正 M2a 归档后的相对链接 |

### Testing

- [OK] [OK] 第 5 步树：lint、typecheck、Vitest 52 文件 954 项、全页面 Playwright 158 项；提交后内嵌产物漂移门禁为空。
- [OK] [OK] 干净提交树 npm ci 后真实服务 13 项；ALAC 调整后隔离 PostgreSQL 播放与样本集成 18 项、真实服务 13 项。

### Status

[OK] **Completed**

### Next Steps

- 用户安排 macOS Safari、Windows Chrome 或 Edge、任一系统 Firefox 三组手工实播（10-06-m2a-acceptance-followup）。
- Safari 通过后恢复 ALAC；下次推送 main 后确认 CI 四个作业通过。


## Session 26: M2b 派生播放：ALAC 经无损派生播放
<!-- trellis-session: v=2 fp=a81ab8b6621c7242 -->

**Date**: 2026-10-10
**Task**: M2b 派生播放：ALAC 经无损派生播放
**Branch**: `main`

### Summary

按获批的 M2b 规划实施 ALAC 无损派生：ffmpeg 两遍转换并核对 PCM、受管缓存、预检长轮询与 202、前端“正在准备”与预取、真实服务与 CI 自动化、规范与 ADR 决策记录；真实库 84 个 ALAC 核对全部成功；任务归档，歌词任务待启动。

### Main Changes

- 后端：playback/derivation（缓存、任务、ffmpeg 适配与核对）、预检与音频的派生分支、三个新错误码、ROOMUSIC_FFMPEG_PATH 与 ROOMUSIC_MEDIA_CACHE_MAX_MIB、缓存目录与音乐根的重叠检查
- 前端：解码器与状态码核对、正在准备（最小间隔 1 秒、11 分钟上限）、预取下一首、忙与失败文案、信号路径；内嵌产物重新构建
- 自动化与文档：真实服务覆盖 ALAC 派生播放，CI 经脚本取得同一个 ffmpeg；新增派生合同，更新播放合同、前端规范、ADR 0004、路线图、README；归档并修正相对链接；未推送

### Git Commits

| Hash | Message |
|------|---------|
| `ef306f7` | feat(playback): 无损派生缓存与 ffmpeg 适配 |
| `5f184ef` | feat(playback): ALAC 经无损派生播放 |
| `5ac806b` | test(playback): 真实服务覆盖 ALAC 派生播放 |
| `8e7657d` | docs(playback): 同步 M2b 派生合同、决策与部署说明 |
| `386f4fe` | docs(m2b): 派生任务收尾——真实库核对、实施结果与验收对照 |
| `b2bf80f` | docs(m2b): 去掉任务文档末尾多余的空行 |
| `11ef404` | chore(task): archive 10-09-classical-m2b-derived-playback |
| `2022884` | chore(task): 修正 M2b 派生任务归档后的相对链接 |

### Testing

- [OK] 隔离 PostgreSQL 与固定 ffmpeg 上 go test -race（playback、app、platform、catalog、library）通过；导出的暂存树上重复通过
- [OK] Vitest 52 文件 1011 项、Playwright 166 项、真实服务（Go + 隔离 PostgreSQL + Chromium）16 项通过；内嵌产物与无环境变量的重新构建逐文件一致
- [OK] 真实库只读核对：84 个 ALAC 转换并核对 PCM 全部成功（98.7 秒，只记汇总）

### Status

[OK] **Completed**

### Next Steps

- 用户装好 ffmpeg（scripts/ffmpeg-pinned.sh 或发行版的包）后重启服务，播放几首真实 ALAC 确认
- 启动歌词任务 10-09-classical-m2b-lyrics；三组手工实播与下次推送后的远端 CI 见 10-06-m2a-acceptance-followup


## Session 27: M2b 歌词：内嵌歌词提取与逐行同步显示
<!-- trellis-session: v=2 fp=fbfa9f826334c5df -->

**Date**: 2026-10-11
**Task**: M2b 歌词：内嵌歌词提取与逐行同步显示
**Branch**: `main`

### Summary

扫描时提取内嵌歌词并保存，接口读取时解析 LRC，正在播放页改为“封面 | 歌词”两栏并逐行同步、点击跳转；真实服务与真实库只读核对；用户确认布局后归档。

### Main Changes

- 后端：解析器提取 Vorbis LYRICS/UNSYNCEDLYRICS、MP4 ©lyr、ID3v2.3/2.4 USLT（按版本读取帧大小、同步消除与帧标志）；迁移 0015 增加 tracks.lyrics 与来源两列及约束；catalog.ParseLyrics 与 GET /api/v1/tracks/{id}/lyrics；CUE 虚拟轨没有歌词
- 前端：歌词端点与严格解码器、会话内不过期的查询、同时间戳分组与二分定位（40 毫秒容差）、歌词面板（加载、错误重试、空态、纯文本、同步；跟随与暂停跟随、回到当前行、点击与键盘跳转、roving tabindex、无 aria-live）、正在播放页两栏布局；内嵌产物重新构建
- 自动化与文档：带歌词的 FLAC 与 ALAC 合成素材与真实服务用例，真实库只读统计测试（TestRealMusicLyricsCoverage）；新增歌词合同规范，更新扫描、catalog REST、前端规范、DESIGN.md、README、路线图与父任务；归档并修正相对链接；后续验收清单增加“歌词面板”一行

### Git Commits

| Hash | Message |
|------|---------|
| `17cb41b` | chore(task): 启动 M2b 歌词 |
| `d6c9fd4` | feat(library): 扫描时提取内嵌歌词 |
| `62023a1` | feat(player): 歌词接口与逐行同步显示 |
| `ca17fa3` | test(player): 真实服务覆盖歌词显示 |
| `63099c8` | docs(player): 同步歌词合同与规范 |
| `c25a70b` | test(library): 增加真实库歌词覆盖的只读统计 |
| `a42f6f9` | test(player): 覆盖歌词面板“回到当前行”的键盘操作 |
| `7a1f863` | docs(m2b): 记录歌词任务的实施结果与验收对照 |
| `b1cfdf1` | chore(task): archive 10-09-classical-m2b-lyrics |
| `470e87c` | docs(m2b): 修正歌词任务归档后的相对链接 |

### Testing

- [OK] [OK] 隔离 PostgreSQL 上相关后端包的 go test -race 通过（含扫描写入/更新/清空、约束、接口 401/400/404/200、API 样本）；每个提交前在导出的暂存树上重复构建与测试
- [OK] [OK] Vitest 54 文件 1073 项、Playwright（路由模拟）182 项、真实服务（Go + 隔离 PostgreSQL + Chromium）21 项通过；内嵌产物与无环境变量的重新构建逐文件一致
- [OK] [OK] 真实库只读核对（只记汇总）：284 个音频文件中 134 个带歌词（MP4 84、FLAC 50），同步 110、纯文本 24，无解析失败，与调研一致；全部 Markdown 相对链接 488 个、断链 0

### Status

[OK] **Completed**

### Next Steps

- 重新扫描一次真实库，歌词才会出现（已入库曲目迁移后没有歌词）；84 个带歌词的文件是 ALAC，要装好 ffmpeg（scripts/ffmpeg-pinned.sh 或发行版的包）并设置 ROOMUSIC_FFMPEG_PATH 后重启，才能播放
- 三组手工实播（含新增的“歌词面板”一行）与下次推送后的远端 CI 见 10-06-m2a-acceptance-followup
- 下一阶段待用户选择并先做规划：P（服务端播放历史）、M3（修正覆盖层）；CUE 精确分轨仍延后


## Session 28: P 服务端播放历史
<!-- trellis-session: v=2 fp=bca6977fc6bc4833 -->

**Date**: 2026-10-11
**Task**: P 服务端播放历史
**Branch**: `main`

### Summary

按用户隔离地保存真实播放：播放器按真正出声的时间累计，达到 min(曲长/2, 4 分钟) 带幂等键上报；概览新增“最近播放”并可清除自己的历史；真实服务与浏览器覆盖；用户确认布局后归档。

### Main Changes

- 后端：迁移 0016 的 play_history 表（用户与幂等键唯一、曲目引用不级联）；catalog 两个公开读取函数；internal/listening（门槛校验、幂等记录、分批读取最近播放、清除）与三个接口，写路由先同源再鉴权，只作用于会话用户
- 前端：ListenTracker 与引擎的访问语义（新访问/继续、拖动与跳变不算）、playRecorder 依赖、上报器（2/10/30 秒同键重试、4xx 放弃、50 个上限、会话结束作废）、概览 RecentPlaysPanel（卡片、清除确认与失败、焦点与状态提示）；内嵌产物重新构建
- 测试与文档：对引擎、上报器、provider 接线与区块做过变异检查；Playwright 新增 13 项（五种宽度、axe、纯键盘），真实服务新增 2 项；新增播放历史合同，同步运行合同、目录结构、播放器与状态管理规范、DESIGN、路线图与父任务；归档并修正相对链接

### Git Commits

| Hash | Message |
|------|---------|
| `b611c13` | docs(listening): 规划服务端播放历史（阶段 P） |
| `733248f` | chore(task): 启动 P 服务端播放历史 |
| `ecf0bb1` | feat(listening): 播放历史的数据表与记录规则 |
| `ee05b97` | feat(listening): 播放历史接口 |
| `7de1750` | feat(player): 达到规则的播放上报到服务端 |
| `e94b77d` | feat(listening): 概览显示最近播放 |
| `d70f7e9` | test(listening): 真实服务覆盖播放历史 |
| `8718340` | docs(listening): 同步播放历史合同与规范 |
| `e65c286` | docs(listening): 记录播放历史的实施结果与验收对照 |
| `04cd0bc` | chore(task): archive 10-11-classical-p-play-history |
| `48e422e` | docs(listening): 修正播放历史任务归档后的相对链接 |

### Testing

- [OK] 隔离 PostgreSQL 上 internal/listening、catalog、app、cmd 的后端测试与 go vet 通过；Vitest 59 文件 1192 项、Playwright（路由模拟）197 项、真实服务 23 项通过；lint 与类型检查通过
- [OK] 内嵌产物在导出的暂存树上重新构建后与提交逐文件一致；全部 Markdown 相对链接 513 个、断链 0；task.py validate 通过

### Status

[OK] **Completed**

### Next Steps

- 概览“最近播放”在 Safari、Firefox 与 Windows 浏览器里的实际渲染，与三组手工实播一起做（10-06-m2a-acceptance-followup 已加“最近播放”一行）；下次推送后的远端 CI 同样见该任务
- 已接受的限制：上报失败后等待重试的记录，在随后清除历史时不会被取消（窗口约 42 秒），见播放历史合同第 5 节
- 下一阶段待用户选择并先做规划：M3（修正覆盖层与受治理操作）；M5（历史页面、收藏、歌单）在 Core 1 之后；CUE 精确分轨仍延后


## Session 29: M3 修正覆盖层与受治理操作
<!-- trellis-session: v=2 fp=5d760a14b87d0998 -->

**Date**: 2026-10-14
**Task**: M3 修正覆盖层与受治理操作
**Branch**: `main`

### Summary

管理员能修正发行的八个字段、把版本归入其他专辑或移出为独立专辑，并撤销自己最近的修改；修正存放在独立的表里，重扫不覆盖；每次成功的修改写一条只有管理员可读的操作记录；用户确认布局后归档。

### Main Changes

- 后端：迁移 0017（operations、release_corrections、releases.edit_revision、release_effective 视图）；新包 internal/operations（共同写入流程 Run：操作者加幂等键的 advisory lock 串行与重放、同键异义冲突、同一事务写记录、错误分类；撤销与展示的 Kind 接口）与 operations/httpapi；catalog 的字段修正与人工归组执行器及逆执行器；四个接口（修正的读写、归组、操作记录列表、撤销）与错误码；所有读取经有效值视图
- 前端：features/governance（命令状态机 useReleaseCommands、编辑发行信息、归入与移出、操作记录页、专辑详情折叠的“修改记录”、撤销确认与焦点兜底）；折叠面板抽成 shared/ui 的 Disclosure；“人工修正”标记；内嵌产物每个前端提交都重新构建
- 测试与文档：后端集成测试覆盖视图、修正状态迁移、归组、撤销、并发与真实重扫；Vitest 与 Playwright（五种宽度、axe、纯键盘）；真实服务用例 governance.real.ts（页面改标题后重扫仍是修正值、归组与撤销、成员 403）；新增受治理操作合同，同步规范、DESIGN、ADR 0002/0003 决策记录、路线图与父任务；归档并修正相对链接

### Git Commits

| Hash | Message |
|------|---------|
| `f182760` | docs(governance): 规划修正覆盖层与受治理操作（阶段 M3） |
| `c8c7e7c` | feat(governance): 迁移 0017、操作记录包与发行有效值视图（M3 第 1 步） |
| `1a36869` | feat(governance): 发行字段修正接口与有效值读取（M3 第 2 步） |
| `b054ec3` | feat(governance): 人工归组接口（M3 第 3 步） |
| `bf62b00` | feat(governance): 撤销与操作记录接口（M3 第 4 步） |
| `0fcb636` | feat(governance): 前端编辑发行信息（M3 第 5 步） |
| `9c28da3` | feat(governance): 前端调整归组（M3 第 6 步） |
| `b2e17ce` | feat(governance): 前端操作记录与修改记录（M3 第 7 步） |
| `53c886e` | test(governance): 真实服务用例（M3 第 8 步） |
| `20c7913` | docs(governance): 同步受治理操作的规范、设计与路线（M3 第 9 步） |
| `084c1d0` | style(governance): 操作记录里的“撤销”与同列文字左对齐（M3 第 10 步） |
| `60f9373` | docs(governance): 记录 M3 的实施结果与验收对照 |
| `d82cd73` | chore(task): archive 10-11-classical-m3-governed-corrections |
| `f5a9ecf` | docs(governance): 修正 M3 任务归档后的相对链接 |

### Testing

- [OK] [OK] 隔离 PostgreSQL 上 gofmt、go vet、go test -race ./internal/operations/...、catalog、identity、app、cmd 的测试通过（含接口样本逐字比较）；步骤 1–4 期间另跑 library、listening、playback 与逐项变异检查
- [OK] [OK] lint、tsc、Vitest 67 文件 1358 项、Playwright（路由模拟）220 项、真实服务 27 项（治理 4 加播放 23，两种文件顺序都验证）通过；内嵌产物在导出的暂存树上重新构建后与提交逐文件一致；Markdown 相对链接 560 个、断链 0；task.py validate 通过

### Status

[OK] **Completed**

### Next Steps

- 成员写操作接入操作记录（G6，独立小任务，补幂等键并写记录，不提供撤销）；M4 规划（认领、整组合并拆分、默认版本、问题处理）待用户指示
- F1 手工实播与 F3 远端 CI 仍按用户裁决延后；浏览器验证只有 Linux Chromium 的自动化证据，没有人工读屏验证
- 已接受的限制：被扫描判定为新发行时不继承旧发行的修正与归组（认领属于 M4）；播放队列里已有条目的专辑名与专辑艺人不随修正即时更新（刷新页面或重新加入队列后用新值）


## Session 30: M4 默认版本与版本对比
<!-- trellis-session: v=2 fp=8bc353b0fc71645f -->

**Date**: 2026-10-15
**Task**: M4 默认版本与版本对比
**Branch**: `main`

### Summary

管理员可以为多版本专辑指定默认版本（成为代表版本：卡片、列表排序、打开专辑与版本导航跟着变），可改回自动选择与撤销，失效时静默回到“年份最早”；所有成员可以对比一张专辑的各个版本（发行字段与格式汇总，不做音质结论）；用户确认布局后归档。

### Main Changes

- 后端：迁移 0018（release_groups 的 default_release_id 与组自己的 edit_revision，operations 放宽类型与目标类型约束）；代表版本规则加默认项，生效与失效由排序得出，扫描与归组都不改它；POST /release-groups/{id}/default 的执行器（先锁发行后锁组、not_in_group、no_changes 比较已存的值）与撤销、展示的 Kind；GET /release-groups/{id}/comparison（固定三条查询、总时长有一首未知即为 null、格式组合上限 32）；operations 的撤销响应改为目标类型加目标 ID，列表按专辑筛选并与发行取并集
- 前端：版本导航的“默认版本”标签与“对比版本”对话框（字段 × 版本，差异下划线加读屏文字，窄屏堆叠）；命令第五种意图 default，成功后先导航再刷新，页面停在刚才的版本；组详情刷新期间暂缓地址规范化（视图读 isFetching，顺带修好 M3 归组落到别的版本的问题）；操作记录的专辑列、改动行、组筛选与“修改记录”的并集；内嵌产物每个前端提交重新构建
- 测试与文档：后端集成测试覆盖生效与失效、写入状态、撤销、并发不死锁与真实重扫；Vitest、Playwright（五种宽度、axe、纯键盘）与真实服务用例（两张合成 WAV，16/44.1 与 24/96）；新增默认版本与版本对比合同，同步规范、DESIGN、ADR 0002/0003 决策记录、路线图（M4 分五项立项）与父任务；收尾截图后修正三处布局并加断言；归档并修正相对链接

### Git Commits

| Hash | Message |
|------|---------|
| `59ca218` | docs(governance): 规划默认版本与版本对比（M4 第一项） |
| `9aa02aa` | chore(task): 启动 M4 默认版本与版本对比 |
| `4b51a32` | docs(governance): 更正 M3 文档里关于播放队列的措辞 |
| `3b3793b` | feat(governance): 迁移 0018 与操作记录的专辑目标（M4 第 1 步） |
| `71ba314` | feat(catalog): 默认版本的读写接口（M4 第 2 步） |
| `b356463` | feat(catalog): 版本对比接口（M4 第 3 步） |
| `6f35d01` | feat(catalog): 专辑页的默认版本标签与版本对比对话框（M4 第 4 步） |
| `0ad6219` | feat(governance): 设为默认版本、改回自动选择、记录与撤销（M4 第 5 步） |
| `04e2728` | test(e2e): 默认版本与版本对比的浏览器用例（M4 第 6 步） |
| `2864d46` | test(e2e): 默认版本的真实服务用例（M4 第 7 步） |
| `e905586` | docs(catalog): 同步默认版本与版本对比的规范、设计与路线（M4 第 8 步） |
| `c3b13cf` | style(catalog): 默认版本标签、对比表窄屏与操作者列的布局修正（M4 第 9 步） |
| `8e1b4b1` | docs(catalog): 记录 M4 默认版本与版本对比的实施结果与验收对照 |
| `e090231` | chore(task): archive 10-14-classical-m4-default-version |
| `e832ef1` | docs(catalog): 修正 M4 默认版本任务归档后的相对链接 |

### Testing

- [OK] [OK] 收尾时在最终树上、隔离 PostgreSQL 上重跑 gofmt、go vet、go test -race ./internal/operations/...、catalog、app、cmd 与 library 的测试，全部通过（含接口样本逐字比较，核对关键集成用例确实运行）
- [OK] [OK] lint、tsc、全量 Vitest 70 文件 1474 项（导出的暂存树）、Playwright（路由模拟）227 项、真实服务 33 项（新增 6 加治理 4 加播放 23）通过；内嵌产物在导出树上重新构建后与提交逐文件一致；Markdown 相对链接 605 个、断链 0；task.py validate 通过

### Status

[OK] **Completed**

### Next Steps

- M4 的其余各项（问题处理、认领、扫描反馈、Release 合并与拆分）待用户选择并先做规划；成员写操作接入操作记录（M3 的 G6）仍是独立小任务
- F1 手工实播与 F3 远端 CI 仍按用户裁决延后；浏览器验证只有 Linux Chromium 的自动化证据，没有人工读屏验证
- 已接受的限制：失效中的默认版本在页面上看不出来，也不能单独清除；版本对比不做曲目逐首对应；默认版本对所有成员相同（个人偏好属于 M5）


## Session 31: M4 元数据问题处理
<!-- trellis-session: v=2 fp=87f0ff75a35b56a4 -->

**Date**: 2026-10-17
**Task**: M4 元数据问题处理
**Branch**: `main`

### Summary

管理员的“元数据问题”页按版本列出“需要关注”的三类问题（拿不准的字段、同一目录的归组冲突、缺失曲目）与依据；字段沿用“编辑发行信息”，来源与介质类型、归组冲突与缺失曲目可以标记为已核对（可撤销，记住依据，扫描之后情况变了重新提醒，缺失曲目逐首判断）；用户确认布局后归档。

### Main Changes

- 后端：迁移 0019（核对表 release_issue_reviews，operations 放宽类型约束）；“需要关注”拆成三类待处理与已核对（issues.go），没有核对时与原算法逐项相同；依据与依据令牌（JSONB 文本取 SHA-256）；POST /releases/{id}/issue-review 的执行器（issue_changed、no_changes，只锁发行行）与撤销、展示的 Kind，撤销取消核对时先给引用的记录加 KEY SHARE 锁防止死锁；GET /release-issues（状态、类别、summary）与 GET /releases/{id}/issues（字段、归组与同一目录的其他版本、缺失曲目，核对人与时间）
- 前端：governance 新增 /issues 问题页（两栏与顺序导航按容器宽度切换、筛选是链接带数字）、详情三节的核对与取消核对（不弹确认，提示里撤销，issue_changed 重新读取），命令 hook 的第六种意图 review，专辑详情的“查看问题”，操作记录的“核对问题”；governance-data 增加问题与核对的接口行为，Vitest 与受控后端共用
- 测试与文档：后端集成测试覆盖计数、依据与过期、拒绝无副作用、撤销、并发不死锁与合成素材真实重扫；Vitest、Playwright（五种宽度、axe、纯键盘）与真实服务用例（合成 WAV，3/2/2 三种专辑标签）；浏览器与真实服务用例各发现一个问题（核对之后焦点掉回顶部、从记录撤销核对的提示写错），都已修好并补回归；新增核对合同并同步规范、DESIGN、ADR 0002/0003、路线图与父任务；归档并修正相对链接

### Git Commits

| Hash | Message |
|------|---------|
| `edc79c8` | docs(catalog): 规划元数据问题处理（M4 第二项） |
| `5f2a9af` | chore(task): 启动 M4 元数据问题处理 |
| `7aaa14a` | feat(catalog): 迁移 0019 与“需要关注”的三类拆分（M4 问题处理第 1 步） |
| `ea85e9a` | feat(catalog): 核对问题的写入与撤销（M4 问题处理第 2 步） |
| `fea575f` | feat(catalog): 问题列表与版本的问题（M4 问题处理第 3 步） |
| `1e2f63b` | feat(governance): 元数据问题页（只读）（M4 问题处理第 4 步） |
| `6585fb1` | feat(governance): 核对、取消核对与撤销，专辑详情的“查看问题”（M4 问题处理第 5 步） |
| `df93999` | test(e2e): 元数据问题的浏览器用例，核对之后焦点落在这一节的标题上（M4 问题处理第 6 步） |
| `ec522c8` | test(e2e): 元数据问题的真实服务用例，从记录撤销核对时的提示（M4 问题处理第 7 步） |
| `0b074f1` | docs(catalog): 同步元数据问题与核对的规范、设计与路线（M4 问题处理第 8 步） |
| `9e2293a` | docs(catalog): 记录 M4 问题处理的实施结果与验收对照 |
| `a07cd07` | chore(task): archive 10-16-classical-m4-issue-handling |
| `c73d61a` | docs(catalog): 修正 M4 问题处理任务归档后的相对链接 |

### Testing

- [OK] [OK] 收尾时在最终提交的树上、隔离 PostgreSQL 上重跑 gofmt、go vet、go test -race ./internal/operations/...、catalog、app、cmd、library 与 platform 的测试，全部通过（含接口样本逐字比较，19 个问题相关的集成用例确实运行）
- [OK] [OK] lint、tsc、全量 Vitest 73 文件 1544 项（导出的暂存树）、Playwright（路由模拟）234 项、真实服务 38 项（新增 5 加默认版本 6、治理 4 与播放 23）通过；内嵌产物在导出树上重新构建后与提交逐文件一致；Markdown 相对链接 652 个、断链 0；task.py validate 通过

### Status

[OK] **Completed**

### Next Steps

- M4 的其余各项（认领、扫描反馈、Release 合并与拆分）待用户选择并先做规划；认领要包含核对的迁移；成员写操作接入操作记录（M3 的 G6）仍是独立小任务
- F1 手工实播与 F3 远端 CI 仍按用户裁决延后；浏览器验证只有 Linux Chromium 的自动化证据，没有人工读屏验证
- 已接受的限制：候选值顺序变化或文件改名会多提醒一次；核对与修正共用发行的版本号；新建发行（G8）没有核对，问题重新出现


## Session 32: M4 认领
<!-- trellis-session: v=2 fp=960822f77f2ae6e8 -->

**Date**: 2026-10-18
**Task**: M4 认领
**Branch**: `main`

### Summary

管理员的“待认领”页列出已不可见、但留有人工修改的旧版本，按文件路径与目录推荐新版本（也能手动搜索任意可见版本），预览逐项说明后把字段修正、所在的专辑、默认版本与问题核对一次迁给新版本（新版本已有的以新版本为准），走共同写入流程、可撤销，预览令牌拦住预览之后变了的状态；专辑详情在被推荐为接替者时提示“可能接替了已不可见的《…》”。扫描照旧不读写这四类状态。

### Main Changes

- 后端：迁移 0020（放宽操作类型约束，建按来源筛选记录的部分表达式索引，不新增表）；待认领的判定谓词与推荐（路径集合：名下曲目加归组依据，理由 same_files / same_directory）；预览与执行器共用纯函数 buildClaimPlan，令牌只含来源与目标的 ID 加各项的去向；加锁顺序为“记录（KEY SHARE）→ 发行行 → 组行”；撤销恢复两个版本与相关专辑的全部前态且不要求新版本可见；operations.ListParams.Sources 让“修改记录”同时列出以某版本为来源的认领；读取接口 release-claims 与 claim-hints
- 前端：governance 新增 /claims 待认领页（两栏与顺序导航按容器宽度切换，选中的旧版本与新版本写进地址）、推荐单选组与“搜索其他版本”、预览逐项文字与认领按钮、结果提示（带“撤销”与“打开这张专辑”，焦点落在提示上）；命令 hook 的第七种意图 claim；专辑详情的“可能接替了……”提示；操作记录的“认领”行
- 测试：后端集成测试覆盖判定、推荐、四类迁移、拒绝无副作用、令牌、重放、并发、随机交错不死锁、两个撤销交叉的死锁回归、合成素材真实重扫；Vitest、Playwright（五种宽度、axe、纯键盘）与真实服务用例（合成 WAV，6 个）；真实服务用例发现操作记录里来源名字取了旧版本现在的有效标题，已改成取记录里的名字
- 收尾截图复核发现并修了三处布局：操作记录里“撤销”被断成两行（连带时间列的日期断开）、搜索按钮停在输入框上方、窄屏提示的图标单独占一行，各有回归断言与变异检查；文档为守住 24,000 字节拆出 core0-tech-debt.md、governance-components.md、quality-verification-layers.md（原样搬移并核对）

### Git Commits

| Hash | Message |
|------|---------|
| `bc70737` | docs(catalog): 规划认领（M4 第三项） |
| `21fe5e7` | chore(task): 启动 M4 认领 |
| `05de626` | feat(catalog): 认领的迁移与待认领的判定、推荐（M4 认领第 1 步） |
| `6c7bd5b` | feat(catalog): 认领的预览与写入（M4 认领第 2 步） |
| `b809f16` | feat(catalog): 认领的撤销、展示与操作记录（M4 认领第 3 步） |
| `35667b8` | feat(catalog): 待认领的读取接口与真实重扫验证（M4 认领第 4 步） |
| `33f40ea` | feat(governance): 待认领页（只读）与认领记录的展示（M4 认领第 5 步） |
| `6858ac2` | feat(governance): 选择接替的版本、预览、认领与撤销，专辑详情的认领提示（M4 认领第 6 步） |
| `fd1a331` | test(governance): 待认领与认领的 Playwright 用例（M4 认领第 7 步） |
| `3971163` | test(governance): 认领的真实服务用例（M4 认领第 8 步） |
| `fe8e939` | docs(catalog): 同步认领的规范、设计与路线（M4 认领第 9 步） |
| `a89befa` | style(governance): 认领的收尾布局修正（操作记录表、搜索行、专辑详情的提示） |
| `099267c` | docs(catalog): 记录 M4 认领的实施结果与验收对照 |
| `b913986` | chore(task): archive 10-17-classical-m4-claim |
| `2e205b9` | docs(catalog): 修正 M4 认领任务归档后的相对链接 |

### Testing

- [OK] 收尾时在最终提交的树上、隔离 PostgreSQL 上运行 gofmt、go vet、go test -race ./internal/operations/...、catalog、app、cmd 与单独的 library、platform，全部通过（含接口样本逐字比较，认领相关 39 个集成用例 PASS、无 SKIP）
- [OK] lint、tsc（导出的暂存树）、全量 Vitest 77 文件 1616 项、Playwright（路由模拟）246 项、真实服务 44 项（新增 6）通过；内嵌产物与重新构建的逐文件一致；变异检查：真实服务 4 个、来源名字 2 个、布局 6 个，加各步骤的后端与前端变异，均被抓住（其中一个窄时换行的变异存活，原因是那段是死代码，已删除）

### Status

[OK] **Completed**

### Next Steps

- M4 的其余各项（扫描反馈、Release 合并与拆分）待用户选择并先做规划；成员写操作接入操作记录（M3 的 G6）仍是独立小任务；ADR 0002 与 0003 现在是 31,962 与 31,415 字节，下一项要先精简再增补
- 同一个测试数据库上并行运行 library 与 app 两个包时偶尔出现一次 scan_coordination_unavailable（扫描互斥是整个数据库范围的 advisory lock，测试只按 schema 隔离，用第二个会话持有这把锁可稳定复现），本任务没有改动扫描代码，尚无后续任务；单独运行任一包都通过
- F1 手工实播与 F3 远端 CI 仍按用户裁决延后；浏览器验证只有 Linux Chromium 的自动化证据，没有人工读屏验证；已接受的限制：推荐只看路径与目录，认领之后被移走的修正与核对的原记录不能再单独撤销


## Session 33: 路线调整与 C1 删除播放中的定时复核
<!-- trellis-session: v=2 fp=315413b8ca1edb43 -->

**Date**: 2026-11-03
**Task**: 路线调整与 C1 删除播放中的定时复核
**Branch**: `main`

### Summary

代码与架构评估后，用户确认只有一个管理员，裁决路线调整 R1–R5（合并拆分搁置、扫描反馈移到 M5 之后、删除在途复核、新写操作轻量标准并撤销 G6、流程减负），新增 C1、C2 并定 Core 1 收口范围；拆出 ADR 0002/0003 的决策记录；C1 由后端子代理实施并验收归档。

### Main Changes

- 路线：core-1-roadmap.md 新增“2026-11-03 路线调整”，ADR 0002/0003/0004 各补一行决策，父任务完成范围改为 M4 ①–③ 加 C1、C2，M2a 遗留验收的远端 CI 记为已通过（运行 39090601105）
- ADR 0002、0003 的决策记录原样移到 *-decisions.md，拼回后与原文逐字节一致，两份 ADR 降到约 26KB 与 25KB
- C1：删除 RecheckingReader、StillPlayable、Prepared.RootID 等在途复核代码，保留只认生命周期的 LifecycleReader；新请求照常鉴权；同步播放合同与 ADR 0004 正文
- 分工改为 ~/.claude/CLAUDE.md 的约定：主会话规划、派活、验收与提交，后端与测试交给 codex/gpt-6-astra，前端交给 claude-opus-5-5

### Git Commits

| Hash | Message |
|------|---------|
| `3f7ded3` | chore(toolchain): 升级 Node.js 到 24.22.1 |
| `d16c7a1` | docs: AGENTS.md 提示开始工作前先读本机环境说明 |
| `440e3d0` | docs(roadmap): 2026-11-03 路线调整（裁决 R1–R5） |
| `84fa4ad` | docs(adr): 把 ADR 0002、0003 的决策记录原样移到单独文件 |
| `641bdef` | chore(task): 规划并启动 C1，建立 C2 子任务 |
| `89ab4b4` | refactor(playback): 删除播放中的定时复核（C1） |
| `4219475` | docs(task): 记录 C1 的实施结果并启动 C2 |
| `400ed9d` | chore(task): archive 11-03-classical-c1-drop-stream-recheck |

### Testing

- [OK] C1：隔离 PostgreSQL 上 go test -race ./internal/playback/... ./internal/app/...，180 个顶层测试（90 个集成）、598 项子测试 PASS、零 SKIP，含接口样本与真实 ffmpeg 派生；gofmt、go vet、依赖规则测试通过
- [OK] 文档：改动的 Markdown 相对链接与锚点无断链，task.py validate 通过；未跑前端与全量后端测试（改动不涉及）

### Status

[OK] **Completed**

### Next Steps

- C2 进行中：后端 B1（拆 catalog/ingest）与前端 F2/F3（播放引擎显式状态）并行；之后 B2、B3、B4、B5 与前端验收 F4，然后 Core 1 收口
- 提交 207d1aa 的说明里“按现状固定”的“状”字被写成了乱码，按约定未 amend，待用户决定
