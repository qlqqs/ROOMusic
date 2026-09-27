# 实施计划

## 顺序与分工

- [x] 用户审阅最终方案，记录选择后运行 `task.py start`。
- [x] 加载前端规范及设计打磨参考，更新新增交互的设计锚。
- [x] Trellis 实施子代理完成播放状态、本地保存与边界测试。
- [x] 实现收藏、历史、歌单及曲目动作，接入我的音乐页面。
- [x] 接入播放器、队列、歌词、显式演示入口与本地图片。
- [x] 完成响应式、键盘焦点、错误和空态。
- [x] Trellis 检查子代理复核需求、定向测试和静态检查；主会话审查设计、规范与结果。
- [x] 浏览器检查桌面和移动端，记录截图与实际运行 URL。

实施与验收完成，Git 提交及归档等待提交方案确认；本轮没有推送或部署。

共享文件依次修改，避免同时改动 `main.tsx`、`styles.css` 和 catalog 模型。主会话协调范围并保留用户其他修改。

## 计划验证

在 `frontend/` 内运行：

```bash
npm run test -- src/features/player src/features/personal-library src/features/catalog/model/display.test.ts src/features/catalog/components/catalog.test.tsx src/release_filters.test.ts
npm run lint
npm run typecheck
npm run build
```

新增测试路径按最终实际文件调整；若修改 decoder，补跑 `src/api.test.ts`。优先定向验证，不运行后端及仓库全量测试。

Playwright 在 375px、900px、1440px 验证浏览、搜索、播放、收藏、歌单增删改、队列边界、刷新恢复、演示隔离、键盘退出及焦点、图片加载、无遮挡。使用受控 API 响应复核真实登录、权限和错误路径，不将模拟结果声称为真实服务集成验证。启动 Vite 并确认 URL 可访问，端口占用时换端口。

## 风险与交付

检查旧队列导出迁移与全局样式影响；真实 API 权限合同不能被演示状态替代。交付说明实际命令与结果、浏览器截图、模拟播放无音频输出及本地数据无跨设备同步。
