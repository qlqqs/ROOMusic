# 执行计划：SlimProto 多台同步编组

任务分支 `task/slimproto-sync-groups`，在仓库根工作；后端分两段顺序派活（第二段依赖第一段的 zones 接口），测试由另外的代理写。

1. [ ] 后端 A（Codex `gpt-6.2-astra` high）：design §1、§3、§5 的 zones 侧——迁移 0079、store、编组接口与不变式、`device_grouped` 守卫、DTO、组音量与同步微调、分发器、`ManagedGroupStream`/`GroupMembers`/`ReportGroupMember`、最小采样率协商、httpapi 端点。
2. [ ] 测试 A（`gpt-6.2-sol`）：S1–S3，API 样本，受影响的现有测试。
3. [ ] 后端 B（`gpt-6.2-astra`）：design §2、§4——slimproto 协议原语与时钟、协调器、upnp 成员占用与级联、`Acquire` 复用判断修正、重新入组回调。
4. [ ] 测试 B（`gpt-6.2-sol`）：S4–S6，`protocol_test`/`clock_test`/slimproto 集成测试同步。
5. [ ] 真实播放器同步测试设施（`gpt-6.2-sol`）：`scripts/test-slimproto-sync.sh`。
6. [ ] 主代理：前端（design §6）、浏览器截图核对；spec（播放区协议 §1/§10、服务端 §9、slimproto 合同、upnp 占用）。
7. [ ] 提交、合入本地 main、归档。

验证命令（范围上限，按改动收窄）：

```bash
cd backend && mise exec -- go vet ./internal/zones/... ./internal/devices/... ./internal/app/
mise exec -- go test ./internal/zones/... ./internal/devices/slimproto/... ./internal/devices/upnp/...
cd .. && sg docker -c "ROOMUSIC_TEST_RUN='Zones|SlimProto|UPnP|Migration|^TestPackageDependencyDirections$' mise exec -- ./scripts/test-integration.sh"
sg docker -c scripts/test-slimproto.sh && sg docker -c scripts/test-slimproto-sync.sh
cd frontend && mise exec -- npm run typecheck && mise exec -- npx vitest run src/features/player src/app && mise exec -- npx playwright test e2e/lan-devices.spec.ts
```

回滚点：后端 A、后端 B、前端各一个提交；迁移 0079 有 down。

## 完成记录（2026-11-25）

本轮最后一次改动（继续时刻修正、前端输入框宽度）之后实际运行：
- `go vet`（zones、devices、app）；`go test -count=1 ./internal/zones/... ./internal/devices/...`；`go test -race ./internal/devices/slimproto/...`（测试代理用本机 golang 镜像）。
- `ROOMUSIC_TEST_RUN='SlimProto|Zones|UPnP|Migration|^TestPackageDependencyDirections$' ./scripts/test-integration.sh`：133 个顶层用例通过，含 0079 迁移往返。
- `sg docker -c scripts/test-slimproto-sync.sh`（默认 600 秒）：无漂移 p95 0.35 ms；暂停后继续峰值 0.65 ms；跳转、同格式换曲、跨采样率接续、5±2 ms 网络抖动均 0.35 ms；成员重连后与 ROOMusic 重启后 0.20 ms；成员 +100 ppm 时 p95 9.24 ms、峰值 10.34 ms、5 次 strm a。成员掉线期间主设备持续出声。
- `sg docker -c scripts/test-slimproto.sh`（单台回归）：全部通过，352.8/705.6 kHz 默认缓冲持续出声。
- 前端：`npm run typecheck`；`vitest`（player、lan-devices、app、api-contract）2270 通过，2 个与本任务无关的失败（插件设置的既有失败；管家页偶发）；`playwright test e2e/slimproto-groups.spec.ts`（3 通过）。

过程中的修正：
- 第一次真实测量暂停后继续偏差 35.7 ms：继续时按 STMp 的 elapsed 差错开（elapsed 按输出块累加、粒度粗），且该分支覆盖了同步微调。改为继续与开新流同一套时刻后峰值 0.65 ms。
- 同步测试脚本在 ROOMusic 重启后要重新查询 Compose 发布端口。

偏离与未做：
- G9：主设备掉线整组暂停（草稿 G7 的字面包括主设备），列为后续项。
- 主设备行的 `Device.group` 在加入成员的响应里不更新（响应只带主区与被加入设备），下一次设备事件或快照才更新；界面不依赖它。
- 编组只在同为 SlimProto 的设备之间；跨协议编组不做。
