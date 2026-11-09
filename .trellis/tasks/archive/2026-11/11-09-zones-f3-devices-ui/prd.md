# F3 设备与远程控制界面（前端）

父任务：[服务端播放区与远程输出](../11-08-server-playback-zones/prd.md)。设计以父任务的 [design.md](../11-08-server-playback-zones/design.md)、[design-protocol.md](../11-08-server-playback-zones/design-protocol.md)、[design-contract.md](../11-08-server-playback-zones/design-contract.md) 为准，本任务不另写设计。

## 范围与验收

执行步骤、测试范围、验证命令、风险文件与回滚点见父任务 [implement.md](../11-08-server-playback-zones/implement.md) 的“F3”一节；该节的每一项都完成、验证命令通过即为验收通过。

## 依赖

F2 合入集成分支。

## 分工

后端实现 `codex/gpt-6-astra`（high），前端实现 `claude/claude-opus-5-5`（high），测试由独立的 `codex/gpt-6-astra`（high）测试 agent 编写与执行；实现 agent 不写新测试、不启动数据库。子 agent 不提交，主会话验收后提交到集成分支。
