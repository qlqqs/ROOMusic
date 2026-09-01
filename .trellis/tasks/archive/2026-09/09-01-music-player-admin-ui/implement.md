# 执行计划

1. 重构 `main.tsx` 视图结构，保留现有请求函数与状态，增加播放器本地状态、队列交互和分区导航。
2. 重写 `styles.css`，建立深色工作台 token、三栏桌面布局、移动端断点、状态与焦点样式。
3. 运行 `npm run typecheck` 与 `npm run lint`，修复类型和可访问性相关问题。
4. 运行 `npm run build` 与 `npm test`，确认生产构建及既有 API 测试。
5. 用 Vite 启动页进行桌面/移动端快速检查，确认无溢出和核心操作可见。

风险文件：`frontend/src/main.tsx`、`frontend/src/styles.css`。回滚点为重构前的这两个文件。
