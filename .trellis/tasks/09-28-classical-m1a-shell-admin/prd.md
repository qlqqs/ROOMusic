# M1a：Classical 外壳与管理界面

> 状态：planning，未启动。2026-09-28 由[路线修订任务](../archive/2026-09/09-28-core-1-roadmap-revision/design.md)（第二部分）创建，本文是 PRD 初稿；
> 启动前需按 Trellis 流程完成规划并获批准。父任务见 [Classical 重设计 / Core 1](../09-27-classical-core-1/prd.md)。

## 目标与用户价值

前端源码已于 2026-09-27 删除。本任务按 Classical 设计与目标目录结构重建客户端，交付可日常使用的外壳、
认证和现有管理能力，并恢复 CI 的真实构建门禁。

## 依据

- [ADR 0001](../../../docs/architecture/adr/0001-frontend-foundations.md) 的决策记录（D3）。
- [frontend/DESIGN.md](../../../frontend/DESIGN.md) 与 `.trellis/spec/frontend/` 下的各项规范。
- [Core 1 路线](../../../docs/architecture/core-1-roadmap.md) 矩阵中标注 M1a 的行。

## 范围

- **脚手架**：TanStack Router + Query；CSS Modules + Classical 全局变量；Fontsource 自托管 Cormorant Garamond / Lora / Noto Serif SC 的 400、600 字重（复核许可）；依赖改为精确锁定版本，替换 `package.json` 中的 `latest`。
- **`shared/api`**：Cookie 凭据、请求 ID、错误信封 decoder；401 结束会话，403 保留会话。
- **认证**：初始化、登录、登出、会话恢复。
- **外壳**：侧栏导航与身份区；播放条等尚未实现的能力隐藏或明确禁用。
- **目录管理**：新增、停用、恢复，携带 revision 与幂等键。
- **扫描**：启动、取消、状态轮询、诊断。
- **成员**：列表、新建、停用、撤销会话。
- **概览**：只展示有真实来源的数据。
- **CI**：删除“前端重置”兜底，恢复 `npm run build` 与内嵌资产漂移检查。
- **接口防漂移**：Go 集成测试产出真实响应的 JSON 样本，前端 decoder 测试读取这些样本，CI 检查样本漂移。

## 前置依赖

没有硬前置，可与 B0、阶段 I 并行。建议 F1、F2 先于或同期完成；F2 完成前，不把目录操作的跨目标重试视为安全功能。
M1b 依赖本任务的外壳与数据层。

## 验收标准（初稿）

- 真实 API 主路径（初始化 → 登录 → 目录 → 扫描 → 成员 → 登出）可用；权限、错误、空态可见且可恢复；没有模拟成功。
- 主要操作可用键盘完成，焦点可见；375、900、1440 宽度下无遮挡、无整页横向溢出。
- `npm run lint`、`npm run typecheck`、`npm run test -- --run`、`npm run build` 通过；内嵌资产无漂移；CI 前端作业恢复真实构建。

## 验证方式

上述命令，加 Playwright 在三种宽度下的冒烟检查（使用受控响应，不接触真实音乐目录）。

## 风险

依赖版本组合的兼容性；字体包体积与布局位移；切换身份时的缓存清理；原型小字号的可读性。

## 范围外

专辑列表与详情（M1b）；播放（M2a）；元数据修正与审批（M3）；个人库（P、M5）；后端接口变更（F1 是独立任务）。

## 待规划事项

- ADR 0001 第 4–5 项：会话代次与跨标签页失效、依赖精确版本与浏览器矩阵、聚焦重新验证与重试策略。
- URL 形状，以及旧 hash 链接是否需要兼容。
- JSON 样本的生成位置与 CI 作业归属。
