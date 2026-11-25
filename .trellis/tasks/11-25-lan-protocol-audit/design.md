# 设计：局域网协议审查的修复

审查结论、证据、修复方案和复现测试都在 `research/audit.md`（下文写作“审查 Cn”），这里只记取舍与分组。主代理已抽查 C5（`devices/upnp/output.go:110` 的 `Acquire` 持有 `s.mu` 等 `old.done`；`stopOutputs` 在调用方持锁时等 `done`）与 C7（`ptp/follower.go:96` 单个样本偏离超过 5 ms 就把样本计数清零，`:109` 要重新攒 3 个样本），两条都属实。

## 修复范围

| 项 | 审查 | 做法 | 本次 |
| --- | --- | --- | --- |
| F1 | C7 | PTP 跟随端在最近若干次测量（如 8 次）里取往返最短的样本作偏移；离群样本只丢弃、不重置；连续多次（如 4 次）偏离同一方向超过阈值，才认定主钟跳变并重置 | 做 |
| F2 | C5 | 接管：在 `s.mu` 内取消旧输出、登记“交接中”，释放锁后等待 `done`，再加锁重新核对设备、授权与占用状态，状态已变就返回相应错误。`stopOutputs` 的调用方（设置、移除、授权、登出、空闲释放）改为锁内取消、锁外等待 | 做 |
| F3 | C5 风险 | 托管输出的订阅不是被主动取消而是被关闭时，写 Warn 日志并按同一会话重新订阅；重订阅失败按既有离线路径释放 | 做 |
| F4 | C1、C12 | mDNS、SSDP 每 30 秒比对网卡集合，按差异增删套接字；没有局域网网卡时不算启动失败，等网卡出现后再启动工作协程，并写一次 Info 日志；接收循环遇到非超时错误时写 Warn 日志，在下一轮重开该网卡的套接字。PTP 在网卡集合变化时重开端口。网卡枚举改为可在测试中替换的包级函数变量 | 做 |
| F5 | C2 | mDNS 定向查询改为 RFC 6762 §6.7 的传统单播查询：从临时端口发出，在同一套接字上收应答；对本机目标保留组播查询；去掉全进程排队 | 做 |
| F6 | C1 次要 | mDNS 定向查询按目标所在网段选网卡（F5 改完后自然覆盖，只需核对） | 做（随 F5） |
| F7 | C8 风险 | AirPlay 心跳改为连续 3 次失败才结束会话，单次期限保持 2 秒 | 做 |
| F8 | C8 | 播放中从 PTP 降级到 NTP 时写 Warn 日志，并通知输出层记日志。不新增界面字段 | 做 |
| F9 | C13 | 部署文档补一张“局域网设备所需入站端口”表：5353/UDP、1900/UDP 及单播回包、AirPlay 计时与控制端口（随机高端口）、PTP 319/320，以及 SlimProto 3483（下一个子任务加入时补） | 做 |
| F10 | C11 | Cast、AirPlay 输出在设备换地址时重建连接 | 不做：现状是断开并暂停播放区，用户重新选择即可恢复；为此改两种输出的生命周期，收益不抵风险。记入后续候选 |

C3、C4、C6、C9、C10 已满足，不改代码（PRD R4）。

## 分组与并行

两组互不重叠的文件，各用一个实现代理、一个独立的工作目录与分支，最后由主代理依次合入任务分支。

| 组 | 项 | 文件 |
| --- | --- | --- |
| A 网络层 | F1、F4、F5、F6 | `internal/ptp/`（`follower.go`、`clock.go`、`network.go`、`service.go`）、`internal/devices/mdns/`、`internal/devices/upnp/discovery.go`、`internal/devices/upnp/ssdp.go`、`internal/devices/cast/registry.go` 与 `internal/devices/airplay/registry.go`（只限 mDNS 启动方式变化带来的调整） |
| B 输出生命周期 | F2、F3、F7、F8、F9 | `internal/devices/upnp/output*.go`、`internal/devices/upnp/service.go`、`internal/devices/cast/output.go`、`internal/devices/airplay/sender.go`、`internal/devices/airplay/clock.go`、`deploy/production/README.md`、`docs/install-nas.md` |

如果某组发现必须改动另一组的文件，停止并在汇报里说明，由主代理协调。

## 测试

- 每项修复配一个修复前失败、修复后通过的测试，按审查里给出的复现测试写；测试由另一个代理编写（实现代理不写新测试）。
- F2 的复现测试是 PostgreSQL 集成测试，放在 `internal/app/upnp_access_integration_test.go` 的接管用例旁。
- F5 的判据用“查询源端口不是 5353”这类确定性断言；“连续查询全部成功”可能间歇通过，不作唯一判据。
- F3 的判据是“订阅被关闭后会重新订阅”，不是“订阅会被关闭”。

## 兼容与回滚

不改接口、表结构和设置。每组一个提交，可单独回退。
