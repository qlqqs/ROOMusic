# 执行计划

1. 实现代理（Codex gpt-6.2-astra），独立工作目录 `../ROOMusic-drift`，分支 `task/slimproto-drift-compensation`：
   - 实现 zones 接口与过滤工厂接入、渲染层补偿器、slimproto 控制与时间轴换算、app 接线；
   - 不写新测试，跑 `go build ./...`、`go vet` 和受影响包里已有的测试。
2. 测试代理（Codex gpt-6.2-astra）：
   - 单元测试：补偿器、协调器的 synctest 用例；
   - 修改真实播放器夹具：sync 套件的漂移阈值改为 p95 ≤ 1.5 ms、峰值 ≤ 3 ms，且要求无 `strm a`；skip 套件改为测补偿点；
   - 运行 `go test` 受影响包、`ROOMUSIC_TEST_RUN` 限定的 PostgreSQL 集成测试（编组相关）、`scripts/test-slimproto-sync.sh`、`scripts/test-slimproto-skip.sh`。
3. 主代理：
   - 审阅改动与越界情况，抽查 WAV；
   - 更新 `slimproto-device-contracts.md` §8 与 `playback-zone-lan-contracts.md` §11，父任务 PRD 与 README 同步；
   - 合入，归档。

## 回滚点

- zones 过滤工厂不注入时，行为等同现状，`correct()` 回到原逻辑前的提交可以单独回退。
