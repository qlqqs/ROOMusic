# 执行计划：SlimProto 服务端（单台）

前置：`11-25-lan-protocol-audit` 已合入本地 `main`（它改动 `devices/upnp/output.go`、`service.go` 的锁与输出生命周期，本任务在其之上接入第四种协议）。

## 分支与工作目录

任务分支 `task/slimproto-squeezelite`，从合入审查后的 `main` 切出，在仓库根工作。后端与前端分两段，前端在后端接口定型后由主代理实现。

## 步骤

1. [ ] 切任务分支；用 `scripts/ffmpeg-pinned.sh` 的 ffmpeg 实测 FLAC 编码器能接受的最高采样率（352800、384000、705600），结果写进 `research/ffmpeg-flac-rates.md`，据此确定 design §3.2 的封顶值。
2. [ ] 派后端实现（Codex `gpt-6.2-astra` high）：design §1–§4、§7，包括迁移 0078、`slimproto` 包、`upnp` 接入、`httpapi` 的设置与格式字段、API 样本（`backend/internal/app/testdata/api-samples/upnp/slimproto-*.json`）。实现代理不写新测试。
3. [ ] 主代理审阅：协议字节与调研一致、无锁内网络 IO、写队列有界、来源过滤、迁移往返。
4. [ ] 派测试（Codex `gpt-6.2-sol` high，另一个代理）：design §6 的单元测试、进程内假播放器与 `upnp` 层集成测试、迁移往返（按 `database-verification.md`）。
5. [ ] 派真实播放器测试设施（Codex `gpt-6.2-sol` high）：`scripts/test-slimproto.sh` 与 `scripts/slimproto/`（Squeezelite 镜像、计时程序、Compose），覆盖 PRD A1–A3、A5、A6。参照 `scripts/test-airplay.sh`。
6. [ ] 主代理实现前端：设置开关与 `port_in_use` 提示、设备行、音质设置、解码器与契约测试；浏览器截图核对（`docs/development-environment.md` 的 Playwright 与中文字体说明）。
7. [ ] 返工：只重跑失败的测试与改动直接影响的包。
8. [ ] spec：`upnp-device-contracts.md` 加 SlimProto 一节（若超过 24 KB 先按主题拆分），部署合同与端口表加 3483；README 功能列表、`docs/install-nas.md` 写明播放器的服务器地址设置。
9. [ ] 提交、合入本地 `main`、清理分支、归档。

## 验证命令（范围上限，按实际改动收窄）

```bash
cd backend
mise exec -- go vet ./internal/devices/... ./internal/app/...
mise exec -- go test ./internal/devices/slimproto/... ./internal/devices/upnp/...
cd .. && ROOMUSIC_TEST_RUN='UPnP|SlimProto|Migration' ./scripts/test-integration.sh
sg docker -c scripts/test-slimproto.sh
cd frontend && npm run typecheck && npm test -- lan-devices api-contract
```

前端命令以 `frontend/package.json` 的实际脚本名为准。

## 回滚点

- 迁移 0078 有 down；回退代码加 down 迁移即可。
- 后端与前端各一个提交；前端可单独回退，后端开关默认在总开关之下，不影响未启用的实例。

## 完成记录（2026-11-25）

本轮实际运行（最后一次改动之后）：
- `go vet`（devices、zones、playback/render、app）；`go test -count=1 ./internal/zones/... ./internal/devices/...`；`ROOMUSIC_TEST_FFMPEG=<固定 ffmpeg> go test -count=1 ./internal/playback/render/...`。
- `ROOMUSIC_TEST_RUN='SlimProto|Migration|UPnP|^TestPackageDependencyDirections$|^TestPostgreSQLCastProtocolStopsDiscovery$' ./scripts/test-integration.sh`：73 个顶层用例通过，含 0078 迁移往返。
- 前端：`npm run typecheck`；`vitest`（lan-devices、player、api-contract、`src/app/lan-devices-settings.test.tsx`）；`playwright test e2e/lan-devices.spec.ts`（7 个通过，截图在 `temp/lan-device-shots/slimproto-*`）。
- 前端全量 vitest：除两项与本任务无关的失败外通过。`src/app/plugins-settings.test.tsx` 的“外部源里的可选插件密钥不应标为 ROOMusic 内置”在干净的 main 上也稳定失败（既有问题）；`src/app/steward.test.tsx` 的一项在全量并发时偶发失败，单独运行通过。

沿用之前结果：
- `sg docker -c scripts/test-slimproto.sh`（真实 Squeezelite 2.0.0，默认缓冲）：全部用例通过，352.8 kHz 与 705.6 kHz 原采样率持续出声。该次运行时 `-frame_size 4096` 是无条件的；之后改为只对 48 kHz 以上生效，48 kHz 及以下恢复 ffmpeg 默认（与第一次真实播放器运行时的 44.1/48 kHz 行为相同，那次 44.1k 用例通过）。

过程中的修正：
- 705.6 kHz 起初只能在 64 MiB 输出缓冲下播放；曾误判为采样率问题而封顶 384 kHz，重测 352.8 kHz 同样卡住，查明是 ffmpeg 默认 FLAC 块大小（352.8 kHz 起 32768 帧）超过 Squeezelite 的解码预留。改为高于 48 kHz 时固定 4096 帧，上限恢复 768 kHz。
- 前端漏跑 `src/app/lan-devices-settings.test.tsx`（解码器新增必填字段使 12 个旧用例失败），由测试代理补样例后通过。

未做：DoP/原生 DSD 直通；边车镜像只提供 Dockerfile 由用户构建，没有发布预构建镜像。
