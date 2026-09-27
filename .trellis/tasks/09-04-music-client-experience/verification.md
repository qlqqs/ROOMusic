# 验证记录

## 环境

- 功能分支：`feat/music-client-experience`。
- 独立 Vite 预览：`http://localhost:5174/`，保留已占用的 5173 服务。
- Playwright 使用本机缓存包，Chromium 依赖通过现有临时库目录提供；中文字体通过 `/tmp/roomusic-fonts.conf` 指向已有字体。未修改系统依赖。
- axe-core 安装于 `/tmp/roomusic-browser-tools`，仅供浏览器验收，不增加产品依赖。

## 已执行

- `git diff --check`：通过（规范更新阶段）。
- `python3 ./.trellis/scripts/task.py validate .trellis/tasks/09-04-music-client-experience`：上下文清单通过。
- `npm run dev -- --host 0.0.0.0 --port 5174 --strictPort`：服务启动成功。
- Playwright 基线访问：真实服务未登录时正确呈现登录页，截图 `/tmp/roomusic-before.png`。

## 增量审查关注点

- 普通成功提示须自动消退，持久化错误保留恢复入口。
- 合法 API 长标题必须经过适配与存储校验后仍能刷新恢复。
- 未知时长不启动模拟计时，旧身份定时器不得更新新身份状态。
- 封面错误状态须在图片地址变化时恢复。

## 第一轮浏览器结果

`browser-check.mjs` 已完成播放计时与暂停、进度和音量、模式切换、收藏、歌单创建重命名删除、刷新暂停恢复、歌词空态与焦点恢复、队列清空及专辑搜索恢复。375px、900px、1440px 无横向溢出；演示全程无 API 请求与运行异常。axe-core 对桌面首页和移动端队列的 WCAG A/AA 检查未报告违规。

`browser-session-check.mjs` 使用受控 REST 响应验证真实用户甲/乙与演示身份隔离，返回原账号能恢复收藏和暂停队列。1200 字曲名成功持久化，但发现个人库布局被最小内容宽度撑开，已要求修复并回归。

视觉第二轮修复点：移动端模拟状态标签与随机按钮重叠，改为独立行；手机端新增音量设置入口。首轮中文方框源于浏览器运行环境缺字体，已配置已有中文字体后重截图。

## 最终检查

独立 `trellis-check` 检查修复了管理、证据、扫描请求的 401 清理遗漏，并给管理初始化请求增加取消和过期响应保护。前端 403 仍显示操作错误，保留当前会话；会话为空时停止扫描轮询与头条详情请求。

在 `frontend/` 实际执行并通过：

```bash
npm run test -- src/features/player src/features/personal-library src/features/catalog/model/display.test.ts src/features/catalog/components/catalog.test.tsx src/release_filters.test.ts
npm run lint
npm run typecheck
npm run build
```

定向测试为 6 文件、38 项，旧队列测试随旧语义移除，由新播放器模型测试覆盖替换/追加、随机、循环、边界等行为。生产静态资产同步至 `backend/cmd/roomusic/web/`。没有运行全量或后端测试，因为改动集中于前端及其生成资产。

在仓库根目录执行并通过：

```bash
git diff --check
env FONTCONFIG_FILE=/tmp/roomusic-fonts.conf LD_LIBRARY_PATH=/tmp/cliproxy-carpool-browser-20260904/playwright-libs/root/usr/lib/x86_64-linux-gnu node .trellis/tasks/09-04-music-client-experience/browser-check.mjs
env FONTCONFIG_FILE=/tmp/roomusic-fonts.conf LD_LIBRARY_PATH=/tmp/cliproxy-carpool-browser-20260904/playwright-libs/root/usr/lib/x86_64-linux-gnu node .trellis/tasks/09-04-music-client-experience/browser-session-check.mjs
```

最终浏览器补充验收：重复曲目独立移动/移除、下一首插入、手机端音量及静音、状态标签与控制键无几何交叠。1200 字曲名在 1440px/375px 均成功刷新恢复且无横向溢出。普通用户无管理请求，管理写请求 403 保持会话/播放器、401 返回登录并移除播放器，均通过。认证与管理请求使用受控 REST 响应，未操作真实用户和音乐目录。

截图与自动报告位于 `/tmp/roomusic-verification/`：`desktop-home.png`、`desktop-playlist.png`、`375-home.png`、`375-queue.png`、`375-music.png` 及 900px、长文本截图；`report.json` 记录演示检查结果和 axe 零违规结果。人工截图复核确认封面渲染、中文可读、曲目/控件无异常重叠。

## 交付边界

可体验地址：`http://localhost:5174/?demo=1#/`。四张专辑、16 首曲目为虚构演示内容；真实音频、下载、歌词服务和云同步不在范围内。收藏和歌单仅保留在当前浏览器。当前代码已完成但未提交，提交及任务归档待用户确认提交方案。
