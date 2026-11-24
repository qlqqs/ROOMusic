# 实施计划：AirPlay 2 投放

在 NAS 一键安装与 Chromecast 合入之后开始。工作目录 `~/workspace/.worktrees/ROOMusic/airplay-output`，分支 `feat/airplay-output`。
规模大，后端分三段实现，每段后由测试代理补测试、主代理审阅：

1. 发现、注册表、RTSP、bplist、HAP 配对（对 shairport-sync 完成 pair-verify 与 SETUP）。
2. RTP 推流、zones 推流输出、RAOP 路径、音量与元数据、托管输出与占用（对两个接收端出声）。
3. `roomusic-ptp` 与 Compose、回退逻辑、设备差异表。

然后前端（主代理）、生产镜像演练、文档与合同。

## 验证命令（范围上限）

- `go test ./internal/devices/airplay/... ./internal/ptp/... ./internal/zones/...`
- `sg docker -c scripts/test-airplay.sh`
- 生产 Compose 检查与镜像演练
