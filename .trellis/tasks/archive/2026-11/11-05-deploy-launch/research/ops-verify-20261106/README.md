# 2026-11-06 运维补验记录

执行时段：2026-11-06 04:49:35–04:59:29 UTC。证据目录权限 700；未建 Trellis 任务，未提交 Git，本执行器未写入仓库文件。凭据和会话仅在 `/tmp/roomusic-drill/`，现已删除；未保存音乐图片、正文、密码、Cookie 值或令牌。备份文件名按容器 Asia/Shanghai 时区生成，证据时间采用 UTC。

## 结果表

| 项目 | 结果 | 关键证据 |
| --- | --- | --- |
| A1 | 通过 | 正式项目 compose restart 后 13.305 秒三个服务全部 healthy；healthz/readyz 200、setup_required=true，三个卷创建时间不变。`a1-result.json`、`a1-compose-restart.log`。 |
| A2 | 通过 | `kill -KILL 2862416` 宿主机执行成功；无 stop/start 干预，RestartCount 0→1，PID 2862416→2863626，6.433 秒恢复 healthy，接口仍 200。`a2-result.json`、`a2-events.log`。 |
| A3 | 仅配置核对 | docker 为 enabled；app/postgres/backup 均 unless-stopped、running/healthy，不处于手动停止状态。未重启机器或 Docker。`a3-result.json`。 |
| B1 | 通过 | 最新正式备份 005718-manual.dump，2.844 秒完成 restore check，迁移 max/count 30/30，两侧其余六表均 0。`b1-result.json`、`b1-restore-check.log`。 |
| B2 | 通过 | 复用正式 06450d0ce2d7 镜像（无打标、无构建）；演练启动 17.024 秒。管理员初始化并登录，/music 完整扫描 19.126 秒成功；68 发行、284 曲目；收藏 2、歌单 1、条目 3；手动备份 551708 字节。`b2-result.json`、`b2-db-counts.json`、`b2-manual-backup.log`。 |
| B3 | 通过 | 演练备份 125331-manual.dump，2.786 秒完成 check；8 项全部一致：迁移 max/count 30/30，users=1，sessions=2，roots=1，releases=68，tracks=284，artworks=15。`b3-result.json`、`b3-restore-check.log`。 |
| B4 | 通过 | 删除歌单后 0 个歌单/0 条目，取消收藏后 1 个收藏；恢复前生成 125409-manual.dump 安全备份；只对 roomusic-drill 执行 production 恢复，10.998 秒 app healthy；随后三个服务均 healthy。恢复为 2 收藏/1 歌单/3 条目；原歌单身份、名称、曲目顺序相同，原密码新会话登录 200。`b4-restore-production.log`、`b4-restore-result.json`、`b4-login-api-result.json`、`b4-all-healthy.json`。 |
| C1 | 通过 | release_artworks=15，全部带 Cookie GET 200；内嵌 7、文件夹 8，其中多碟 1。ALAC 预检 derived/200、音频 206，原生时间 0→2.801631 秒；缓存 0→2 个 FLAC，共 58465403 字节。`c1-artwork-baseline.json`、`c-before-artwork-http.json`、`c-before-alac.json`、`c1-cache-after-play.json`。 |
| C2 | 通过 | 校验卷名字/项目/卷标签，停止并移除演练 app 容器后只删除 roomusic-drill_app-data；7.000 秒重新拉起 healthy。卷创建时间 04:51:15→04:57:05 UTC；演练数据库启动时间、卷创建时间和计数不变。`c2-volume-delete-precheck.json`、`c2-result.json`。 |
| C3 | 通过（现有库样本） | 丢卷后 DB 关联仍 15，15 个接口全部 404；三个抽查详情页显示失败占位和重试按钮、曲目列表正常、pageerror=0。完整重扫 14.081 秒后 15/15 均 200，全部内容 SHA-256、关联身份相同；内嵌 7/7、文件夹 8/8、多碟 1/1。再次播放同一 ALAC，0→2.822985 秒，缓存重新生成，数量及总大小再次为 2 个文件/58465403 字节。`c-after-artwork-http.json`、`c-artwork-detailed-evidence.json`、`c-lost-pages.json`、`c-after-pages.json`、`c-after-alac.json`。 |
| D | 通过（资源与基线；工作区外部变更另列） | 演练容器、网络、两个命名卷及 backup 附带匿名卷均删除，18090 空闲，临时目录/凭据删除；正式镜像标签未改。开发基线完全一致，正式 healthy、setup_required=true、三个卷创建时间及备份列表/权限/mtime/大小/SHA-256 全部一致。`d-final-result.json`、`baseline-before.json`、`baseline-after.json`。 |

完整命令清单见 `commands-summary.md`；逐次调用见 `commands.log`。不运行全量测试：本轮无代码实现，只验证用户指定运维路径。

## 问题与边界

1. 未发现本次重启、备份核对、真实恢复、封面重建、ALAC 派生路径的部署脚本或应用缺陷。丢卷后的 404/失败占位是本次观察到的降级行为，不是页面崩溃。
2. **CUE 覆盖缺口**：本库有效 CUE 虚拟曲目和 CUE 发行均为 0。两次扫描均显示 100 条 cue_reference 诊断（展示可能截断），不把它当作有效 CUE 样本，也不将“扫描成功”写成无诊断。68 个发行中原本就有 53 个没有封面，本次不存在新丢失封面的差异；不能推广为所有 CUE/其他音乐库都保证恢复。属于文档结论需要明确的验证边界，未修改文件。
3. **测试执行器自身三处修正**：setup/admin 不自动建会话，必须再登录；歌单曲目摘要用 track.track_id 而非 track.id；Docker inspect 挂载数组顺序无语义，按目标/源排序后比较。均只修改 /tmp 下的测试执行器或补充只读复核，不改应用。首轮 D 误报保留于 d-result.json，最终结论见 d-final-result.json，过程见 test-harness-notes.md。
4. **工作区并发/外部变化**：开始 Git 干净、HEAD ca21446；结束 HEAD 未变，但 .gitignore 和 README.md 出现修改，mtime 分别 04:51:48、04:58:11 UTC。本执行器没有写入这些文件，修改来源未独立核实；没有还原或接管这些改动。不宣称仓库结束时干净。证据 concurrent-worktree-observation.log。
5. A3 是配置核对，不是机器重启实测。未重启 Docker、机器或修改系统配置。正式 restore 仅使用 check，production 仅用于 roomusic-drill。

## README 建议替换文字（仅建议，未改文件）

> 2026-11-06 演练确认：在本机现有音乐库中，丢失 `/data` 后执行完整扫描，可重建原有 15 张封面（内嵌 7、文件夹 8，包含 1 个多碟发行），恢复前后封面内容哈希一致；ALAC 派生缓存也能重新生成。重建依赖原始音乐文件及封面来源仍然可读。本次没有有效 CUE 样本，不能保证其他所有音乐库和场景均可完整重建，也不能以此替代备份。

## 前后基线

### 开发项目

| 项目 | 开始 | 结束 |
| --- | --- | --- |
| 采集时间（UTC） | 2026-11-06 04:49:35 | 2026-11-06 04:59:29 |
| 容器状态 | running | running |
| 启动时间 | 2026-10-26T03:13:45.497579973Z | 完全相同 |
| postgres-data 创建时间 | 2026-09-01T09:54:51Z | 完全相同 |
| 迁移最大版本 | 12 | 12 |
| users | 1 | 1 |
| releases | 68 | 68 |

### 正式项目

- app/postgres/backup：healthy→healthy；setup_required：true→true；/healthz、/readyz 始终在各步骤完成后为 200。
- app 启动时间：2026-11-05T16:55:48.074910197Z→2026-11-06T04:50:15.698080453Z，RestartCount 0→1，符合 A1/A2 预期。
- postgres 启动时间：2026-11-05T16:55:40.559684376Z→2026-11-06T04:50:02.271147274Z；backup：2026-11-05T16:55:48.080284958Z→2026-11-06T04:50:02.207576535Z，符合 A1 预期。
- 三个实际卷：postgres-data 创建时间 2026-11-05T16:55:40Z；app-data 及 backup 镜像附带的匿名卷（77766f505966…）均 2026-11-05T16:55:47Z，前后逐项不变。两个命名卷通过 Compose 项目标签查询，第三个匿名卷通过容器挂载归属核对。
- 正式备份目录：仍是 005546、005716、005718 三个 manual.dump 和 .last-success；无新增或删除，所有大小/权限/uid/gid/mtime/SHA-256 不变。
- 正式镜像 06450d0ce2d7 与 current 标签、镜像 ID 均不变；未创建演练镜像标签，没有构建或删除正式镜像。
