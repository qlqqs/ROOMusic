# 实施计划：Chromecast 投放

工作目录 `~/workspace/.worktrees/ROOMusic/chromecast-output`，分支 `feat/chromecast-output`；在匿名统计合入之后开始（统计的设备种类要加 `cast`）。

1. 后端实现（Codex，gpt-6.2-astra）：mDNS、cast、zones 泛化、MP3 编码、迁移、接口与样本、假接收端。
2. 后端测试（Codex，gpt-6.2-sol）：帧编解码、状态机、发现、托管输出与占用、编码、迁移、接口集成、用假接收端的端到端。
3. 前端（主代理）。
4. 生产镜像演练：同机 host 网络下发现并控制假接收端。
5. 文档：README、部署说明、`docs/development-environment.md` 的假接收端、合同。
