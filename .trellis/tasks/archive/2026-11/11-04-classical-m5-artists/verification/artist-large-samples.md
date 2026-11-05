# ⑤ 巨型 API 样本清单

已落盘96个小型艺人增量样本，包含补齐的12个状态/M3小样本。小样本**不包含** `artists/queue-truncated`。
该场景另存完整 `{status,body}` JSON 到仓外，不提交一万项响应到 Git。

- 配置：`ROOMUSIC_ARTIST_LARGE_SAMPLES_DIR`。
- 相对路径：`artists/queue-truncated.json`。
- 本轮完整路径：`/home/qlqq/.local/share/roomusic-evidence/m5-artists/api-samples/artists/queue-truncated.json`。
- 来源：真实 app handler `GET /api/v1/artists/{id}/queue`；10001个可用候选返回10000项，另有1个 missing。
- 数据处理：复用现有 API recorder 的敏感路径/凭据检查、UUID/时间规范化及 `{status,body}` 编码；保留全部10000个 items，不手工构造或裁剪响应。
- 常规测试不配置目录时仍执行完整计数、去重、present、全候选随机排序后截断断言；配置目录后，UPDATE=1 写入，未启用 UPDATE 时逐字比较，文件缺失即失败。
- 外置写入只管理上述单个文件，不清理目录，不调用仓内样本 finish，不重写仓内小样本。

以下命令在仓库根执行，须等主控释放隔离数据库后运行：

```sh
source /tmp/roomusic-m5-artists-test.env
export ROOMUSIC_ARTIST_LARGE_SAMPLES_DIR=/home/qlqq/.local/share/roomusic-evidence/m5-artists/api-samples
cd backend
ROOMUSIC_UPDATE_API_SAMPLES=1 go test -race ./internal/app -run '^TestPostgreSQLArtistQueueTruncatedResponseSample$' -count=1 -json
ROOMUSIC_UPDATE_API_SAMPLES=0 go test -race ./internal/app -run '^TestPostgreSQLArtistQueueTruncatedResponseSample$' -count=1 -json
```

主控已用真实隔离服务生成并完成无UPDATE逐字比较，两次race均PASS、0 SKIP。完整队列规范化样本15069139字节，原始HTTP正文9139046字节；未裁剪10000个items。证据：仓外artist-complete-samples-update.json、artist-complete-samples-compare.json。
