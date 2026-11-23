# E2 实施

主代理直接实现（前端）。分支 `feat/auto-curation-e2`。

1. api：例外 preview 解码；会话列表端点与解码器移入 curation；query-keys；hooks（例外、采纳、不采纳、会话、设置类型扩展、轮询）。
2. 文案：`curation-text.ts` 加模型状态、原因、例外改动、错误文案。
3. 组件：`curation-model-section.tsx`、`curation-exceptions-section.tsx`；进度组删“待定”行；页面与路由 `exception_page`。
4. steward：会话页 `kind=curation` 的四处处理；导出 `useStewardModels`。
5. 替身：curation-data、fake-curation、fake-steward、e2e api-mock 与 steward。
6. 测试：`app/curation-settings.test.tsx` 加“模型判断”“例外”两组用例；steward 会话页一例；e2e 冒烟加两组；截图。
7. 规范：interface-layout、state-management。
验证：`npm run -s typecheck`、`npx eslint src e2e`、相关 Vitest、Playwright 冒烟。
