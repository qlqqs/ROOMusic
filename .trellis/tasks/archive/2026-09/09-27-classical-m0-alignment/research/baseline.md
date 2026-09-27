# 创建任务时的事实与建议记录

## 来源与边界

记录日期：2026-09-27。仓库基线：`6cf76ec`。
用户提供的上一轮会话摘要是候选路线来源，本次对关键仓库事实做了最小核对。
本记录不把上一轮建议当作用户逐项批准，也不重复进行完整代码审计或外部技术选型研究。

## 已核对事实

| 事实 | 仓库证据 |
| --- | --- |
| 原型与 Classical 设计系统已入库 | `frontend-desgin/ROOMusic 原型.dc.html`、`frontend-desgin/ROOMusic 设计探索.dc.html`、`Sidebar.dc.html`、`PlayerBar.dc.html`、`_ds/classical-d3de10a0-5b67-4da7-b5b3-8d49f26a63f1/styles.css` |
| 设计尚未形成生产设计合同 | `frontend-desgin/github.md` 记录“尚未对齐 frontend/DESIGN.md”；本次检查 `frontend/DESIGN.md` 与 `frontend/src/` 均不存在 |
| CI 对前端缺失有显式兼容 | `.github/workflows/ci.yml:70-90` 检测入口后选择真实构建或占位资源检查 |
| 设计删除已提交，不再是上轮所说的未提交删除 | `git status --short` 初始为空；`6cf76ec` 为设计稿替换提交 |
| 前端规范仍描述旧实现 | `.trellis/spec/frontend/index.md:5-19` 描述源码、模拟播放与演示模式；`player-design-guidelines.md:8-14` 指向旧月刊设计和 hash 路由 |
| 共享和架构文档也有过时事实 | `.trellis/spec/guides/engineering-principles.md` 声称无应用源码；`docs/architecture/architecture-decisions.md` 的当前实现树仍列出缺失前端文件 |
| 现有 API 边界可被直接核对 | `backend/cmd/roomusic/application.go:56-78` 注册 setup/auth/users/roots/scans/releases/artworks 路由 |
| 未匹配路由进入前端处理器 | `backend/cmd/roomusic/application.go:79`；`/api/` JSON 404 是候选后续修复，不属于纯拆包 |
| 扫描新 Release 时创建独立分组记录 | `backend/cmd/roomusic/scanner.go:926-930` 先按来源锚点查找，再创建 `release_groups(id)` |
| 当前用户与署名角色有限 | `backend/migrations/0006_multi_user.sql:1` 为 admin/user；`0012_track_credits_and_release_metadata.sql:39` 为 composer/conductor/performer/producer |
| 扫描观察不按每个字段创建变更集 | `.trellis/spec/backend/agent-and-operation-guidelines.md:93-95` |
| 可复用队列语义已有规范 | `.trellis/spec/frontend/player-design-guidelines.md:34` 区分立即播放、下一首、追加及队列身份 |
| 旧任务尚未关闭 | `.trellis/tasks/09-04-music-client-experience/task.json` 状态为 `in_progress`；其 PRD 沿用旧视觉、模拟播放和本地数据 |
| 依赖声明仍有 latest | `frontend/package.json:14-30`；版本锁定属于后续脚手架/依赖决策，不在本轮修改 |

## 页面能力矩阵初稿

本表用于 M0 核对，不替代将来的端点与错误合同。

| 页面/能力 | 现有依据 | 缺口或待核实项 | 候选阶段 |
| --- | --- | --- | --- |
| 初始化、登录、成员 | setup/auth/users API | 新前端、错误与权限状态呈现；编辑者角色未提供 | M1；角色扩展 M3 |
| 专辑列表与详情 | Release 列表/详情/证据、封面 API | ReleaseGroup 聚合、默认版本和跨版本比较 | M1 基础；M4 版本 |
| 目录与扫描 | roots/scans API、取消与诊断 | 原型的精细进度、定时/增量、暂停和报告不能直接视作现有能力 | M1 现状；增强另划后续子任务 |
| 概览 | 可复用已公开的列表/状态数据 | 不存在的数据不伪造；不得为首页隐含增加统计接口 | M1 简版 |
| 正在播放与歌词 | 无已注册媒体串流接口 | 媒体鉴权、浏览器兼容、CUE/转码、歌词和真实播放器 | M2 |
| 元数据问题 | 候选字段与证据为线索 | 问题处理状态和持久修正写路径；需进一步核对数据模型 | M3/M4 |
| 审批与日志 | 目录操作 API | 通用变更集、审批、检查点、冲突与逆操作 | M3 |
| 搜索、艺人和个人库 | 现有基础检索需沿用 catalog 合同 | 艺人/别名和用户隔离的收藏、歌单、历史 | M5；播放历史可由 M2 先建 |
| Music Steward | 已有模式/权限规范 | 运行时、工具、提案与独立审查未因原型存在而实现 | M6 候选 |

## 上轮建议的保留方式

- 保留 M0 → M1 与 B0 → M2/M3 → M4 → M5 → M6 的分阶段思路；依赖按实际能力说明，不误把任务父子关系当作依赖调度。
- React/TypeScript/Vite、边界 decoder 与同源会话延续；Router/Query、CSS Modules、本地字体作为选型候选。
- ReleaseGroup 实体化、修正覆盖层、通用操作记录作为数据/写路径候选，不提前改库。
- 媒体接口、ffmpeg 派生缓存、歌词解析、PlayerEngine、Media Session 与播放历史作为 M2 研究输入。
- 三种固定角色、艺人/别名、PostgreSQL 搜索增强与服务端个人数据作为后续需求，不提前锁定迁移与接口。
- 原型中的源文件删除、扫描恢复点、硬件无损保证、监听/暂停和外部内容应形成明确落地差异。
- 源设计目录改名与 `support.js` 取舍延期；本轮不移动、不删除资产。

## 尚需验证而非已确认的假设

- `candidate_anchor` / `source_identity` 在重扫、移动、重命名和结构调整中的稳定性。
- 各浏览器对具体容器/编解码器的能力，以及直出、无损转码、拖动和 CUE 分轨的兼容策略。
- 字体分片/许可、第三方库版本、ffmpeg 部署/资源限制与许可证要求。
- Core 1 截止范围、角色权限和操作风险规则，由后续提案与用户决策确定。

上述技术事实应在对应 ADR/子任务内查阅官方资料或测试，不凭上一轮摘要推断。
