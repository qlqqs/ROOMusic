# 前端路由懒加载（意见 4）

## Goal

成员打开应用时不下载只有管理员才用得到的页面代码与样式；管理员进入这些页面时按需加载。

## 已确认事实

- `frontend/src/app/router.tsx` 静态导入全部页面组件，`src` 内没有 `lazy` 或动态 `import()`；构建后入口 JS 约 597 KB、CSS 约 178 KB（`vite.config.ts` 已用 `manualChunks` 拆出 react、tanstack、lucide、vendor）。
- 管理员页面由 `AdminGate` 包裹：`/artist-candidates`、`/issues`、`/claims`、`/settings/library`、`/settings/library/scans/$scanId`、`/settings/members`、`/settings/operations`、`/settings/backups`。
- 路由同时从 feature 入口（如 `features/governance/index.ts`）导入 `parseClaimsSearch` 等查询解析函数；入口文件同时导出组件，组件带 CSS Modules 副作用导入，可能把整块拉回入口包。

## Requirements

1. 上列管理员路由与 `/settings/playback`（DSP 与播放区设置，体积大、非首屏）的页面组件改为 TanStack Router 的路由级懒加载（`lazyRouteComponent` 或 `createLazyRoute` 二选一，全项目统一一种）。`AdminGate`、守卫、`validateSearch` 留在主路由定义中，行为不变。
2. 保证查询解析函数不把页面组件拉进入口包：如有必要，为 feature 增加只导出查询解析与类型的公开入口（例如 `features/governance/search.ts`），并在 `.trellis/spec/frontend/directory-structure.md` 写明这一例外；不直接引用 feature 内部文件。
3. 懒加载期间的等待显示复用现有加载样式；加载失败（断网、部署后旧代码块 404）显示现有错误态并允许重试或刷新，不出现空白页。
4. 成员首屏路由（概览、专辑、艺人、搜索、我的、登录、初始化）保持在入口包内，不引入加载闪烁。
5. 专辑与艺人详情中的管理员对话框（编辑发行、拆分艺人等）本次不拆。

## Acceptance Criteria

- [ ] 构建后入口 JS 与入口 CSS 比改动前小，汇报改动前后的体积；管理员页面的代表性组件（如 `ClaimsView`、`MembersPage`、`BackupsPage`、DSP 设置）不在入口代码块中（按构建产物中的组件特征字符串或 Vite manifest 核对）。
- [ ] `npm run lint`、`npm run typecheck`、`npm run test` 通过；`npm run test:e2e` 通过（含管理员页面的导航与 axe 检查）。
- [ ] 模拟代码块加载失败时，页面显示错误态而不是空白（单元测试或 e2e 覆盖一种）。

## Out of Scope

- 管理员对话框拆分、依赖替换、CSS 重写。
