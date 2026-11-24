# 实施计划：UPnP/DLNA 设备发现与单设备串流

分支 `feat/m8-upnp-renderer`（基于合入艺人资料之后的 `main`），独立工作目录 `~/workspace/.worktrees/ROOMusic/m8-upnp-renderer`。合入本地 `main`，不推送。

## 顺序

1. 后端实现代理（gpt-6.2-astra，high），分两段提交到工作区（不提交 Git）：
   - 段一：`devices/upnp` 的发现、描述、控制、DIDL、局域网出站限制；假渲染器；开关与 `ROOMUSIC_LAN_URL`；注册表与管理接口（列表、手动添加）。
   - 段二：zones 接入（`kind=upnp`、迁移、能力、设备设置、流令牌与免 Cookie 路由、托管输出端、占用与接管、离线与吊销）；接口样本；部署覆盖文件与文档；
     规范：新增 `.trellis/spec/backend/upnp-device-contracts.md`，修订播放区相关规范与 `deployment-environment-contracts.md`。
   - 不写新测试；跑编译、`go vet` 与改动包的既有测试。
2. 后端测试代理（gpt-6.2-sol，high）：发现与过期、SSRF 规则、SOAP 序列、轮询与进度换算（两种 RelTime 行为）、设备端暂停、占用与接管、令牌（HEAD、重连、失效、限速）、
   离线、成员停用、开关关闭时无副作用、迁移往返、接口样本；用假渲染器做端到端（真实渲染会话与 PostgreSQL）。
3. 主代理：前端（设备列表、占用与接管、设备菜单、设置页），前端测试与 Playwright 冒烟，390 与 1440 截图。
4. 主代理验收：开发后端在宿主机运行，起两个假渲染器，两个成员账号走 A2–A5；Compose 配置检查（A9）。
5. 真实设备试用（A11）：请 qlqq 在家里的设备上试，不阻塞合入。

## 验证命令

```bash
cd backend
mise exec -- go build ./... && mise exec -- go vet ./internal/devices/... ./internal/zones/... ./cmd/roomusic-fake-renderer/... ./internal/app/...
mise exec -- go test -race ./internal/devices/... ./cmd/roomusic-fake-renderer/...
# zones 与 app 的数据库用例按 docs/development-environment.md 的隔离做法，只跑相关包与 -run
scripts/check-production-compose.sh
cd ../frontend && mise exec -- npm run check && mise exec -- npx playwright test e2e/player*.spec.ts e2e/settings*.spec.ts
```

## 回退点

- 段一与段二分别提交；前端单独提交。开关默认关，合入后未打开的实例行为不变。
