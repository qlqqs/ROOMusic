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
