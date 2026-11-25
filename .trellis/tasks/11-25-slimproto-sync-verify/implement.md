# 执行计划

1. 实现代理（Codex gpt-6.2-astra）在独立工作目录 `../ROOMusic-slimproto-verify`、分支 `task/slimproto-sync-verify`：
   - 查明版本表：在上游 git 历史里按 `MICRO_VERSION` 找提交；查 moOde 与 Debian 稳定版的版本；写入 `research/squeezelite-versions.md`。
   - 实现镜像覆盖、`sync-clock.py` 的录音开关、`test-slimproto-skip.sh` 和分析、`squeezelite-ref.Dockerfile`、`test-slimproto-versions.sh`。
   - 运行 `sg docker -c scripts/test-slimproto-skip.sh` 和 `sg docker -c scripts/test-slimproto-versions.sh`，把结果写进 `research/skip-audibility.md` 和 `research/squeezelite-versions.md`。
2. 主代理验收：
   - 审阅改动，确认没有越界改到后端、前端或生产 Dockerfile。
   - 自己听或抽查 WAV，核对指标。
   - 不传镜像跑一次 `test-slimproto.sh` 的脚本语法检查（`bash -n`）与 `compose config`；原有两项测试的完整运行沿用矩阵里 1613 那一行的结果。
3. 更新 spec §8.4 和父任务 PRD 的 L6 与进度，合入 `main`，归档。

## 回滚点

- 只改 `scripts/` 和任务文档，直接回退分支即可。
