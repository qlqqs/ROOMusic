# 执行计划：局域网协议审查的修复

## 分支与工作目录

- 任务分支 `task/lan-protocol-audit`，从 `main` 切出，工作目录用仓库根。
- 两组修复各用一个独立工作目录与分支：`../ROOMusic-audit-net`（`task/lan-protocol-audit-net`，A 组）与 `../ROOMusic-audit-out`（`task/lan-protocol-audit-out`，B 组），都从任务分支切出。主代理审阅后依次合入任务分支，再合入本地 `main`（不推送）。

## 步骤

1. [ ] 建任务分支与两个工作目录。
2. [ ] 派 A 组实现（Codex `gpt-6.2-astra` high）：F1、F4、F5、F6。实现代理不写新测试，只运行现有测试。
3. [ ] 派 B 组实现（Codex `gpt-6.2-astra` high）：F2、F3、F7、F8、F9。
4. [ ] 主代理审阅两组改动：越界文件、锁顺序（F2 重新加锁后的核对）、日志字段。
5. [ ] 各派一个测试代理（Codex `gpt-6.2-sol` high），按审查里的复现测试写测试。在修复前的代码上确认失败（`git stash` 或在任务分支基点上运行），修复后通过，并把两次结果写进汇报。
6. [ ] 返工（如有）：只重跑失败的测试与改动直接影响的包。
7. [ ] 合入任务分支，跑一次合并后受影响包的测试；更新 spec（`upnp-device-contracts.md` 的 PTP、mDNS、锁行为描述，部署合同的端口表）。
8. [ ] 提交、合入本地 `main`，清理工作目录与分支，归档。

## 验证命令（范围上限，按实际改动收窄）

A 组：

```bash
cd backend
mise exec -- go vet ./internal/ptp/... ./internal/devices/mdns/... ./internal/devices/upnp/... ./internal/devices/cast/... ./internal/devices/airplay/...
mise exec -- go test ./internal/ptp/... ./internal/devices/mdns/... ./internal/devices/upnp/... ./internal/devices/cast/... ./internal/devices/airplay/...
```

B 组：

```bash
cd backend
mise exec -- go vet ./internal/devices/... ./internal/zones/...
mise exec -- go test ./internal/devices/upnp/... ./internal/devices/cast/... ./internal/devices/airplay/...
ROOMUSIC_TEST_RUN='UPnP' ./scripts/test-integration.sh      # 在仓库根目录；F2 的接管集成测试
```

AirPlay 真实接收端回归（B 组改了心跳与降级，合并后跑一次）：

```bash
sg docker -c scripts/test-airplay.sh
sg docker -c 'scripts/test-airplay.sh --ptp'
```

## 回滚点

- 每组一个提交，可单独 `git revert`。
- F2 改动了锁的持有方式，是风险最高的一项；若集成测试或 AirPlay 回归出现死锁或竞态，先回退 F2，其余照常合入。
