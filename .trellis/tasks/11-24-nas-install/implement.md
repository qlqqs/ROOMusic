# 实施计划：NAS 一键安装

工作目录 `~/workspace/.worktrees/ROOMusic/nas-install`，分支 `feat/nas-install`。

1. 实现（Codex，gpt-6.2-astra）：Compose、单文件版本与派生检查、删除脚本、`check-production-compose.sh`、发布流水线、规范中部署段落的事实同步。不写新测试。
2. 测试（Codex，gpt-6.2-sol）：`test_production_deployment.py` 扩展与 Compose v2.20.1 检查，删除脚本测试。
3. 文档（主代理）：`docs/install-nas.md`、README“快速开始”、部署说明、`.env.prod.example`。
4. 生产镜像演练（测试代理）：从零安装、0.8.0 升级、模拟 NAS 属主。
5. 审阅、合入本地 `main`。

## 验证命令（范围上限）

- `scripts/check-production-compose.sh`
- `python3 -m pytest scripts/tests/test_production_deployment.py -q`（按用例名收窄）
- Compose v2.20.1：`temp/compose-2.20.1 -f compose.prod.yaml --env-file <临时 env> config`
