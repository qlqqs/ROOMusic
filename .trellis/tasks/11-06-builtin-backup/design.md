# 备份内置到应用：技术设计

- 父任务：[部署体验改进](../11-06-deploy-ux/prd.md)；需求：[prd.md](./prd.md)（R1–R11、A1–A9）
- 依据：[后端调研](../11-06-deploy-ux/research/backend-facts.md)、[前端调研](../11-06-deploy-ux/research/frontend-facts.md)
- 衔接：[config-trim 设计](../11-06-config-trim/design.md)（compose 与部署目录）、[versioned-release 设计](../11-06-versioned-release/design.md)（版本号、回退迁移）
- 日期：2026-11-06；起草：设计子 agent（未改任何产品代码）

## 0. 三个子任务共用的名字（三份 design.md 一致，改动须三处同步）

| 名字 | 含义 | 定义方 |
| --- | --- | --- |
| `postgres.Open(ctx, url, ...postgres.OpenOption)` | 连接 + 迁移；新增可选项 | 本任务 |
| `postgres.WithMigrationBackups(postgres.MigrationBackups)` | 注入迁移前备份能力 | 本任务 |
| `postgres.WithLogger(*slog.Logger)` | 执行器写 `platform.migration.*` 事件 | 本任务建，versioned-release 补降级事件 |
| `postgres.MigrationBackups` 接口：`BackupBeforeMigration(ctx, db *sql.DB, change MigrationChange) (string, error)`、`LatestPreMigrationBackup(schemaVersion int64) (string, bool)` | 执行器需要的备份端口（消费方 platform/postgres 定义，backup.Service 实现） | 本任务 |
| `postgres.MigrationChange{Direction, FromVersion, ToVersion, Versions}`；`Direction` 取 `postgres.MigrationUpgrade`（`"upgrade"`）或 `postgres.MigrationDowngrade`（`"downgrade"`） | 本次启动将做的结构变更 | 本任务建类型；降级取值由 versioned-release 产生 |
| 备份来源 `scheduled` / `manual` / `pre_migration` / `pre_restore` | API 与日志里的 `source` | 本任务 |
| 文件名 `roomusic-YYYYmmdd-HHMMSS.dump`（定时）、`-manual.dump`、`-pre-migration-<N>.dump`（N=迁移前库里的最高迁移版本）、`-pre-restore.dump` | 备份目录内的发布文件 | 本任务 |
| `internal/platform/buildinfo`：`buildinfo.Current() Info{Version, Commit}`，ldflags 变量 `buildinfo.version`、`buildinfo.commit` | 版本号来源 | versioned-release 定义，本任务读取 |
| `GET /api/v1/system/version` → `{"version": string\|null, "commit": string\|null}` | 版本号接口（null = 开发版） | 本任务（接口）+ versioned-release（取值） |
| `ROOMUSIC_BACKUP_DIR` | compose 层：宿主机目录或卷名（默认命名卷 `backups`）；容器内应用层固定为 `/backups` | 本任务（应用）+ config-trim（compose） |
| `deploy/production/compose.yaml`、`deploy/production/example.env`、占位符 `__RELEASE_VERSION__` | 发布用部署文件 | config-trim |
| 演练项目 `roomusic-drill`，子网 `10.203.51.0/24` | 隔离演练 | 三者共用 |

## 1. 边界与模块划分

### 1.1 后端

```text
backend/internal/backup/            # 新能力包：备份的唯一所有者（不含 HTTP）
  service.go      Service：进程内状态、调度循环、手动/迁移前/命令行入口、停机等待
  schedule.go     纯函数 Plan：到点、补备份、重试；只依赖 time
  files.go        文件名规则、解析来源、列出、原子发布、保留清理、安全打开下载
  pgtools.go      Runner 接口与 exec 实现：连接参数、pg_dump / pg_restore 调用、stderr 归类
  lock.go         跨进程 advisory lock（会话级，专用 *sql.Conn）
  state.go        备份目录内的状态文件 .roomusic-backup-state.json
  restore.go      恢复：读取备份信息、检查连接与权限、安全备份、重建库、pg_restore、核对计数
  tzdata.go       import _ "time/tzdata"（容器没有 /usr/share/zoneinfo 也能按时区计时）
backend/internal/backup/httpapi/    # /api/v1/system/* 路由：解析、鉴权、映射，不含业务
backend/internal/platform/buildinfo/  # versioned-release 建；本任务只读取 Current()
backend/internal/platform/config/   # 增加 Backup 设置与只读备份配置的 LoadBackup()
backend/internal/platform/postgres/ # 增加 OpenOption、MigrationBackups、MigrationChange；Connect 设置 application_name
backend/cmd/roomusic/
  cli.go          子命令分派（从 reset_password.go:63-80 的 runCommand 抽出）：reset-password、backup、restore、version/--version
  backup_command.go、restore_command.go   只做终端交互、提示与退出码
```

- 依赖方向：`backup` → `platform/{config,postgres}`；`backup/httpapi` → `backup`、`identity`（Guard）、`platform/{httpserver,buildinfo}`。
  `platform/postgres` 不导入 `backup`（端口由消费方定义，见 `.trellis/spec/backend/index.md` “Consumer-owned ports”）。
- 版本接口 `GET /api/v1/system/version` 暂放在 `backup/httpapi`：它唯一的使用者是“系统 › 备份”页。以后“系统”组出现第二个页面时再拆成 `internal/system/httpapi`。
- 组合根 `internal/app/app.go:92-109` 增加 `app.WithBackups(*backup.Service)` 选项；未提供时不注册 `/api/v1/system/*`（路由测试与不需要备份的集成测试保持原样）。

### 1.2 前端

```text
frontend/src/features/system/
  api/endpoints.ts      fetchBackupOverview、startBackup、fetchBuildInfo、backupDownloadPath(name)
  api/decoders.ts       decodeBackupOverview、decodeBackupCommand、decodeBuildInfo（+ decoders.test.ts）
  components/backup-status-section.tsx（+ .module.css）、backup-files-section.tsx、backup-messages.ts
  hooks/use-backup-overview.ts、use-start-backup.ts、use-build-info.ts
  query-keys.ts、index.ts
frontend/src/app/routes/backups-page.tsx    Page + 两个区段
frontend/src/app/router.tsx                 新路由 /system/backups（AdminGate），加进 router.tsx:336 的路由树
frontend/src/app/shell/nav-items.ts         新组“系统”
frontend/src/shared/lib/bytes.ts            formatBytes（+ bytes.test.ts）
frontend/src/test/fake-system.ts            Vitest 替身后端
frontend/src/app/system-backups.test.tsx    页面级测试
frontend/e2e/system-backups.spec.ts         mock 冒烟（1440 / 375 截图）
```

## 2. 数据流

### 2.1 一次备份（所有来源共用 `Service.run`）

```text
取锁（进程内 mutex → advisory lock）
 → stage=directory：目录存在（开发默认目录不存在时 MkdirAll 0700）且可写
 → stage=prepare：找到 pg_dump/pg_restore；构造连接参数；pg_dump 主版本 ≥ 服务端主版本
 → os.CreateTemp(dir, ".roomusic-<时间>-<source>-<pid>.partial.*")，0600
 → stage=pg_dump：pg_dump --format=custom --no-password --lock-wait-timeout=60s --dbname=<去掉密码的连接串>，stdout = partial 文件
 → stage=verify：pg_restore --list，stdin = partial 文件
 → stage=publish：fsync(partial) → os.Link(partial, 目标名)（EEXIST 时等到下一秒换名）→ 删除 partial → fsync(目录)
 → 记 backup.completed；写状态文件（清掉 last_failure）
 → 保留清理（只在成功后）：删除匹配命名规则且 mtime 早于 keep_days×24h 的文件
任一阶段失败：删除 partial；记 backup.failed（source、stage、exit_code、reason）；写状态文件 last_failure；释放锁
```

- pg_dump 的 stdout 直接是 partial 文件的 fd：生产环境是普通文件，可 seek，自定义格式照常写数据偏移（`pg_restore -j` 可用）。
  参数里没有任何宿主机路径，所以开发/测试可以把 pg_dump 换成 docker 包装脚本（见 §5.4）。
- 不覆盖已有文件：`os.Link` 遇到同名返回 EEXIST，等待到下一秒重新取名（沿用 `deploy/production/backup.sh:38-43` 语义）。
- 启动时（`Start`）删除备份目录里 24 小时以前的 `.roomusic-*.partial.*` 残留（进程被杀时留下的）。

### 2.2 迁移前备份（R5，执行器调用）

```text
main → openDatabase(ctx, url, postgres.WithMigrationBackups(backups), postgres.WithLogger(logger))
  postgres.Open：Connect → 迁移事务开始 → pg_advisory_xact_lock(迁移锁) → 探测 schema_migrations 是否存在、各列是否存在（只读系统目录）
    表不存在（全新空库）→ 执行 0001 等全部迁移，不备份
    读历史（缺 name/checksum/down_sql/down_checksum 列时按 NULL 读）→ 计算计划（不依赖回退列是否存在）
    无待执行 → （缺回退列时此时才加列）→ 提交，不备份
    有升级（或 versioned-release 的降级）→ backups.BackupBeforeMigration(ctx, db, change)
        → Service.run(source=pre_migration, 文件名后缀 -pre-migration-<change.FromVersion>)，阻塞等待备份锁
        失败 → 返回错误 → 迁移事务回滚（库不变）→ Open 返回错误 → main 降级启动，/readyz 503
    成功 → 所有 DDL 从这里开始：加回退列（versioned-release §3.3）→ 执行迁移或回退 → 记录 → 提交
```

- **执行器合同·备份前锁规则**（与 versioned-release §3.4 同一条，写进 `database-guidelines.md` 的执行器合同）：
  > 调用迁移前备份之前，迁移事务只允许持有迁移 advisory lock 和不高于 AccessShareLock 的表锁（即只做 `SELECT` 与系统目录查询）；
  > 任何 DDL——包括加回退列 `down_sql` / `down_checksum`、执行 0001、执行 0008——都放在备份成功之后。读历史时缺列按 NULL 处理，计划判定不依赖这些列是否存在。
  > 全新空库要执行 0001，本来就不备份，不受此规则影响。

  理由：pg_dump 在另一个连接上要拿每张表的 AccessShareLock；迁移事务若先做了 DDL（AccessExclusiveLock，持有到事务结束），pg_dump 会等它，而事务又在等 pg_dump 返回，形成跨连接死锁，
  PostgreSQL 的死锁检测看不到（等待链经过客户端进程）。遵守这条规则时，事务持有的锁与 pg_dump 兼容，备份内容就是迁移前的库；第二个 app 实例会卡在迁移 advisory lock 上，不会抢先迁移。
- 万一有意外的锁冲突，pg_dump 的 `--lock-wait-timeout=60s`（§3.5）会让它以 `stage=pg_dump, reason=lock_timeout` 失败，迁移不执行、app 不就绪，而不是永久挂起。
- 执行器只在 `schemaMigrationsTableExists`（`migrate.go:250-256`）为真时调用备份；“全新空库不备份”（R5）由此保证。
- 版本号后缀用迁移前的最高版本 `FromVersion`：降级被拒时，versioned-release 用 `LatestPreMigrationBackup(本版本最高迁移)` 找到
  “从本版本升级出去之前”的那份备份写进日志。

### 2.3 调度（R2）

`schedule.go` 的纯函数（单元测试全覆盖 A2）：

```go
type PlanInput struct {
    Now         time.Time
    Location    *time.Location
    Hour        int       // 0–23
    LastSuccess time.Time // 零值 = 没有成功备份
    RetryAt     time.Time // 零值 = 没有待重试的失败
    Startup     bool      // 本进程第一次计划
}
type PlanResult struct {
    Due  bool      // 现在就该备份
    Next time.Time // 下一次计划时刻（Due 时等于 Now）；用于 next_scheduled_at 与定时器
}
func Plan(in PlanInput) PlanResult
```

规则与 `backup.sh:57-86` 等价：

1. `Startup` 且（没有成功备份或 `Now-LastSuccess ≥ 24h`）→ 立即。
2. 当地时间已到 `Hour` 点且 `LastSuccess` 的当地日期不是今天 → 立即。
3. `RetryAt` 非零：`Now ≥ RetryAt` 时立即，否则 `Next = RetryAt`（即使今天的计划时刻还没到，失败后仍每小时重试，同 `backup.sh:76-77`）。
4. 否则 `Next` = 今天 `Hour:00`（还没到点且今天没成功）或明天 `Hour:00`（`time.Date` 规范化处理夏令时缺口）。

- “成功”包括所有来源：手动或迁移前备份成功后，当天的定时备份不再重复（与旧 `.last-success` 语义相同）。
- `LastSuccess` 取备份目录里**合法备份文件的最新 mtime**（任何来源），不另存：文件只在校验通过后才发布，最新文件的时间就是最近一次成功。
  旧 `backup.sh` 留下的 `.last-success` 不再读取，也不删除。
- 失败：`RetryAt = 失败时刻 + 1h`；成功：清零。`RetryAt` 只在进程内存里；重启后由规则 1 兜底。
- 循环：`timer = min(Next, Now+10min)` 醒来重新计划（应对时钟跳变）；手动备份成功后经 channel 唤醒重新计划。
  定时备份遇到跨进程锁被占（另一进程在备份）不记失败，10 分钟后重新计划。

### 2.4 状态持久化

| 信息 | 存放 | 理由 |
| --- | --- | --- |
| 最近一次成功时间 | 备份文件 mtime（派生） | 不需要额外状态；与文件一致；迁移前（新表还不存在时）也可用 |
| 最近一次失败（时间、阶段、来源） | `<备份目录>/.roomusic-backup-state.json`（0600，临时文件 + rename） + 内存 | 重启后仍能在页面显示；目录不可写时只留在内存 |
| 正在备份（来源、开始时间） | 内存 | 只描述本进程 |
| 待重试时刻 | 内存 | 重启后由“启动补备份”规则覆盖 |

状态文件格式：`{"version":1,"last_failure":{"at":"RFC3339","stage":"pg_dump","source":"scheduled"}|null}`。读到不认识的 `version` 或解析失败时当作没有失败记录并记 `backup.state_unreadable`。

**不用库表的原因**：迁移前备份发生在迁移之前，新表可能还不存在；数据库不可用时页面也看不到；状态和备份放在一起，换机器拷备份目录时一起走。

### 2.5 并发（R6）

- 进程内：`Service.mu` 保护 `running`。同一时刻最多一个备份；手动请求发现正在备份（任何来源）时不新建，返回当前状态（HTTP 200）。
- 跨进程：会话级 `pg_try_advisory_lock($1)` / `pg_advisory_lock($1)`，键 `backupAdvisoryLockKey int64 = 0x524f4f4d4241434b`（ASCII `ROOMBACK`），
  在专用 `*sql.Conn` 上持有到备份结束（advisory lock 本身按数据库区分，不需要第二个键）。

| 入口 | 取锁方式 | 锁被别的进程占用时 |
| --- | --- | --- |
| 定时 | try | 不记失败，10 分钟后重新计划 |
| 手动（HTTP） | try，在返回 202 前同步取得 | `409 backup_busy` |
| 迁移前 | 阻塞（受 ctx 控制） | 等待；日志 `backup.waiting_for_lock` 每 30 秒一次 |
| `roomusic backup` / 恢复前安全备份 | 阻塞（受 ctx 控制） | 终端打印“另一份备份正在进行，等它完成…” |

### 2.6 时区与 tzdata

- `ROOMUSIC_TIMEZONE`（默认 `Asia/Shanghai`）在 `config.Load` / `config.LoadBackup` 时 `time.LoadLocation`，失败即配置错误（`platform.config.invalid`，进程退出 1），与现有配置校验一致。
- `internal/backup/tzdata.go` 写 `import _ "time/tzdata"`（约 450 KB），镜像不装 `tzdata` 包也能解析时区；文件名里的时间按该时区，API 里的时间一律 UTC RFC 3339。

## 3. 接口合同

### 3.1 HTTP（全部仅管理员；鉴权顺序同 `internal/library/httpapi/scans.go:12-36`）

| 方法与路径 | 成功 | 说明 |
| --- | --- | --- |
| `GET /api/v1/system/version` | 200 | 版本号 |
| `GET /api/v1/system/backups` | 200 | 状态 + 列表 |
| `POST /api/v1/system/backups` | 202 新开始 / 200 已在备份 | 写请求：先 `RequireSameOrigin` 再 `RequireAdmin`；无请求体、无幂等键（重复点击得到同一个进行中的备份） |
| `GET /api/v1/system/backups/{name}` | 200（支持 Range） | 下载附件；Go 1.22 mux 的 `GET` 模式同样匹配 `HEAD` |

**`GET /api/v1/system/version` → 200**

```json
{ "version": "0.1.0", "commit": "0123456789abcdef0123456789abcdef01234567" }
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `version` | `string \| null` | 语义化版本，不带 `v`；开发构建（未注入或注入值不合法）为 `null`，页面显示“开发版” |
| `commit` | `string \| null` | 40 位小写十六进制提交号；未知为 `null` |

**`GET /api/v1/system/backups` → 200**

```json
{
  "status": {
    "running": { "source": "manual", "started_at": "2026-11-06T03:00:00Z" },
    "directory": "ready",
    "last_success_at": "2026-11-05T19:00:12Z",
    "last_failure": { "at": "2026-11-05T19:00:03Z", "stage": "pg_dump", "source": "scheduled" },
    "next_scheduled_at": "2026-11-06T19:00:00Z"
  },
  "backups": [
    { "name": "roomusic-20261106-030012.dump", "source": "scheduled", "created_at": "2026-11-05T19:00:12Z", "size_bytes": 1048576 }
  ],
  "total": 1
}
```

| 字段 | 类型 | 取值 |
| --- | --- | --- |
| `status.running` | `object \| null` | 本进程正在进行的备份；`source` ∈ `scheduled` `manual` `pre_migration` `pre_restore`，`started_at` 时间戳 |
| `status.directory` | `"ready" \| "missing" \| "not_writable"` | 每次请求用 `stat` + `access(W_OK\|X_OK)`（`golang.org/x/sys/unix`）检查，不写文件 |
| `status.last_success_at` | `string \| null` | 最新合法备份文件的 mtime；没有备份为 `null` |
| `status.last_failure` | `object \| null` | 最近一次失败，**只在它晚于 `last_success_at` 时返回**（被之后的成功覆盖则为 `null`）；`stage` ∈ `directory` `prepare` `pg_dump` `verify` `publish`；`source` 同上 |
| `status.next_scheduled_at` | `string \| null` | 下次定时备份时刻（`Plan.Next`）；调度未启动时 `null` |
| `backups[]` | 数组，最多 500 项，按 `created_at` 倒序 | `name` 合法文件名；`source` ∈ 四种来源；`created_at` = 文件 mtime；`size_bytes` 非负整数 |
| `total` | 整数 | 目录里合法备份文件总数（> 500 时页面提示只显示最近 500 份） |

列表只收录**符合命名规则的普通文件**（`Lstat`，跳过符号链接、目录、隐藏文件和 partial）。旧文件 `roomusic-YYYYmmdd-HHMMSS.dump` 识别为 `scheduled`，`-manual.dump` 识别为 `manual`（A4）。

**`POST /api/v1/system/backups`**：响应体 `{ "status": <同上 status 对象> }`；202 表示本次开始了一个手动备份（`status.running.source = "manual"`），
200 表示本进程已有备份在进行（返回的是那一个）。备份异步执行，结果由后续 `GET` 读取。

**`GET /api/v1/system/backups/{name}`**：

1. `RequireAdmin`（401/403）。
2. `{name}` 必须完整匹配 `backup.FileNamePattern`：`^roomusic-[0-9]{8}-[0-9]{6}(-manual|-pre-migration-[1-9][0-9]{0,9}|-pre-restore)?\.dump$`；
   不匹配（含 `..`、`/`、`%2F` 解码后的斜杠、反斜杠、空字节）→ `404 backup_not_found`。
3. 经 `os.OpenRoot(备份目录)` 的 `Root.Open(name)` 打开（Go 1.24+，拒绝越出目录与符号链接逃逸），再 `Stat` 要求普通文件；不存在 → 404。
4. 响应头：

| 头 | 值 |
| --- | --- |
| `Content-Type` | `application/octet-stream`（先设置，`http.ServeContent` 不再嗅探） |
| `Content-Disposition` | `attachment; filename="<name>"`（文件名已被正则限定为 ASCII 安全字符，不需要 `filename*`） |
| `Cache-Control` | `private, no-store` |
| `X-Content-Type-Options` | `nosniff` |
| `Content-Length`、`Last-Modified`、`Accept-Ranges` | 由 `http.ServeContent` 设置；不设 `ETag` |

5. 文件包一层生命周期 reader（与 `internal/playback/httpapi/handlers.go:145-177` 相同做法，复制一份到 backup/httpapi，不跨能力导入私有 helper）：停机时中止传输。

**错误码**

| HTTP | code | message | 场景 |
| --- | --- | --- | --- |
| 401 | `unauthorized` | 需要登录 | 未登录（现有 `guard.WriteUnauthenticated`） |
| 403 | `forbidden` | 需要管理员权限 | 非管理员（现有） |
| 403 | `origin_required` / `origin_forbidden` | 请求缺少来源校验 / 请求来源不受信任 | POST 跨源（现有） |
| 404 | `backup_not_found` | 备份不存在 | 下载：名字不合规或文件不存在 |
| 409 | `backup_busy` | 另一份备份正在进行，稍后再试 | POST：另一个进程持有备份锁 |
| 503 | `database_unavailable` | 服务暂不可用 | 降级模式下所有 `/api`（现有 `app.go:148-158`） |

所有 JSON 经 `httpserver.WriteJSON`（`Cache-Control: no-store`）；消息里不含路径、连接串或 pg_dump 原文。

### 3.2 API 样本命名约定（只定约定，由实现时的集成测试生成）

目录 `backend/internal/app/testdata/api-samples/system/`，错误样本放 `errors/` 且以 `backup-` 开头：

| 样本 | 状态 | 内容 |
| --- | --- | --- |
| `system/version-dev.json` | 200 | 测试二进制没有 ldflags：`{"version":null,"commit":null}` |
| `system/backups-empty.json` | 200 | 空目录 |
| `system/backups.json` | 200 | 预置旧命名定时、旧命名手动、迁移前、恢复前各一份（固定文件名与 mtime），带 `last_failure` |
| `system/backups-running.json` | 200 | 假 Runner 阻塞时读取 |
| `system/backups-directory-missing.json` | 200 | `directory: "missing"` |
| `system/backup-started.json` | 202 | |
| `system/backup-already-running.json` | 200 | |
| `errors/backup-not-found.json` | 404 | |
| `errors/backup-busy.json` | 409 | 测试另开连接持有 `ROOMBACK` 锁 |

- 生成：新文件 `backend/internal/app/system_samples_integration_test.go`，`apiSampleRecorder{scope: "system"}`；`ownsSample`（`api_samples_integration_test.go:969`）增加
  `isSystemAPISample(name)`（`system/` 前缀或 `errors/backup-` 前缀），默认记录器排除它们，并补 `TestAPISampleRecorderOwnership` 的用例。
- 确定性：`backup.WithClock` 固定时钟、`backup.WithRunner` 假 Runner、临时备份目录里用 `os.Chtimes` 固定 mtime。
- 前端：`frontend/src/test/api-contract.test.ts` 为每个样本登记 decoder；`frontend/e2e/support/api-mock.ts` 的受控响应取自这些样本。

### 3.3 Go 接口

```go
// internal/platform/postgres（本任务新增）
type MigrationDirection string
const (
    MigrationUpgrade   MigrationDirection = "upgrade"
    MigrationDowngrade MigrationDirection = "downgrade" // versioned-release 产生
)
type MigrationChange struct {
    Direction   MigrationDirection
    FromVersion int64   // 迁移前库里的最高版本
    ToVersion   int64   // 迁移后（本二进制的最高版本）
    Versions    []int64 // 按执行顺序：升级为升序，降级为降序
}
// MigrationBackups 是执行器需要的备份端口；由 internal/backup.Service 实现，main 注入。
type MigrationBackups interface {
    // BackupBeforeMigration 同步完成一次迁移前备份并返回文件名；返回错误时执行器不迁移。
    BackupBeforeMigration(ctx context.Context, db *sql.DB, change MigrationChange) (string, error)
    // LatestPreMigrationBackup 返回迁移前版本为 schemaVersion 的最新迁移前备份文件名。
    LatestPreMigrationBackup(schemaVersion int64) (string, bool)
}
type OpenOption func(*openOptions)
func WithMigrationBackups(backups MigrationBackups) OpenOption
func WithLogger(logger *slog.Logger) OpenOption
func Open(ctx context.Context, databaseURL string, options ...OpenOption) (*sql.DB, error)
// ErrPreMigrationBackup 包装迁移前备份失败；main 据此写日志。
var ErrPreMigrationBackup = errors.New("pre-migration backup failed")

// internal/backup
type Source string // "scheduled" | "manual" | "pre_migration" | "pre_restore"
func NewService(settings config.Backup, logger *slog.Logger, options ...Option) *Service
func WithClock(now func() time.Time) Option   // 测试
func WithRunner(runner Runner) Option         // 测试
func (s *Service) Start(lifecycle context.Context, db *sql.DB)       // 启动调度；数据库可用后调用
func (s *Service) Wait(ctx context.Context) error                    // 停机有界等待
func (s *Service) Overview(ctx context.Context) (Overview, error)
func (s *Service) StartManual(ctx context.Context, db *sql.DB) (Status, bool, error) // bool=本次新开始；ErrBusy
func (s *Service) RunNow(ctx context.Context, db *sql.DB, source Source) (File, error) // 同步、阻塞等锁：CLI 与恢复用
func (s *Service) BackupBeforeMigration(ctx context.Context, db *sql.DB, change postgres.MigrationChange) (string, error)
func (s *Service) LatestPreMigrationBackup(schemaVersion int64) (string, bool)
func (s *Service) OpenBackup(name string) (*os.File, fs.FileInfo, error) // ErrNotFound
var ErrBusy, ErrNotFound error
```

`main.go` 的接线（替换 `main.go:44-67`、`:118-132` 的相应部分）：

```go
backups := backup.NewService(settings.Backup, logger)
database, databaseErr := openDatabase(ctx, settings.DatabaseURL,
    postgres.WithMigrationBackups(backups), postgres.WithLogger(logger))
...
if database != nil {
    backups.Start(applicationContext, database)
    appOptions = append(appOptions, app.WithBackups(backups))
}
// 停机：WaitForCatalogMaintenance 之后、derivations.Close 之前
if err := backups.Wait(shutdownContext); err != nil { logger.Error(..., "event", "backup.shutdown_timeout") }
```

### 3.4 配置（`internal/platform/config`）

| 变量 | 默认 | 校验 |
| --- | --- | --- |
| `ROOMUSIC_BACKUP_DIR` | `<ROOMUSIC_DATA_DIR>/backups`（开发）；生产 compose 固定传 `/backups` | 非空路径 |
| `ROOMUSIC_BACKUP_HOUR` | `3` | 整数 0–23 |
| `ROOMUSIC_BACKUP_KEEP_DAYS` | `14` | 整数 1–3650 |
| `ROOMUSIC_TIMEZONE` | `Asia/Shanghai` | `time.LoadLocation` 成功 |
| `ROOMUSIC_PG_BIN_DIR` | 空 = 在 `PATH` 查找 | 非空时必须是绝对路径；开发与测试指向 docker 包装脚本目录（§5.4） |

- `config.Server` 增加 `Backup config.Backup`；新增 `config.LoadBackup() (config.Backup, error)`：只读 `ROOMUSIC_DATABASE_URL` 与上表，不要求 `ALLOWED_LIBRARY_ROOTS`
  （与 `config.DatabaseURL()`（`config.go:166-172`）同理），供 `roomusic backup` / `restore` 使用。
- compose 层的 `ROOMUSIC_BACKUP_DIR` 是宿主机路径或卷名，只用于插值，不会传进容器；容器里的应用变量由 compose 固定写 `/backups`（见 config-trim 设计）。

### 3.5 pg_dump / pg_restore 调用

- **连接参数**：`ROOMUSIC_DATABASE_URL` 必须是 `postgres://` / `postgresql://` URL（生产 compose 就是）。`net/url` 解析后：
  1. 取出密码（userinfo 与查询参数 `password` 都取），从 URL 中删除；
  2. 查询参数只保留 libpq 认识的白名单（`sslmode`、`sslrootcert`、`sslcert`、`sslkey`、`connect_timeout`、`target_session_attrs`、`options`），
     其余（如测试的 `search_path`、pgx 专有参数）丢弃；
  3. 子进程参数：`--dbname=<去掉密码的 URL>`（只含用户名、主机、端口、库名）；环境变量**只**给 `PGPASSWORD`（有密码时）、`PGAPPNAME=roomusic-backup`、
     `PGCONNECT_TIMEOUT=10`、`LC_ALL=C`，不继承父进程环境。
  - 理由：`/proc/<pid>/cmdline` 对所有用户可读，`/proc/<pid>/environ` 只有同 uid 与 root 可读；容器内只有 uid 1000 一个用户。
    另一选择是 0600 的临时 `PGPASSFILE`（`/tmp` 是 tmpfs），需要转义 `:` 与 `\`，收益不大，不采用。
  - URL 解析失败或不是 URL 形式 → `stage=prepare, reason=unsupported_database_url`。
- **pg_dump**：`pg_dump --format=custom --no-password --lock-wait-timeout=60s --dbname=…`，`exec.CommandContext`，`Cancel` 发 SIGTERM，`WaitDelay=10s`。
  - `--lock-wait-timeout=60s`：pg_dump 开始时要给每张表加 AccessShareLock，默认无限等待。正常运行时 app 只持有与之兼容的行级写锁，真正冲突的只有 DDL；
    60 秒足够等过偶发的短暂排他锁，又远小于镜像健康检查的 5 分钟宽限（§3.7），锁冲突会表现为可诊断的备份失败（`stage=pg_dump`、`reason=lock_timeout`）而不是挂起。
    取值格式与 `SET statement_timeout` 相同（PostgreSQL 文档）。
- **校验**：`pg_restore --list`，stdin 为 partial 文件，stdout 丢弃。
- **stderr**：最多保留 64 KiB，只用来归类 `reason`（`connect_failed`、`auth_failed`、`version_mismatch`、`lock_timeout`（stderr 含 `could not obtain lock`）、`disk_full`、`killed`、`other`），**不写日志、不进 API**。
- **版本检查**（prepare）：`pg_dump --version` 解析主版本，与 `SHOW server_version_num / 10000` 比较；客户端更低 → `reason=version_mismatch`。

### 3.6 PostgreSQL 18 客户端从哪里来（R1）

| 方案 | 做法 | 优点 | 缺点 |
| --- | --- | --- | --- |
| **A（推荐）** | app 阶段改为 `alpine:3.23@sha256:<索引摘要>`，`apk add postgresql18-client` | 一行；依赖库由 apk 处理；`pg_dump`、`pg_restore`、`pg_isready`、`psql` 都在 `/usr/bin`（已在 design-probe 容器核实：2026-11-06 为 18.6） | 小版本随 apk 仓库浮动（主版本固定为 18）；重建镜像可能拿到更新的 18.x，这对客户端是无害的 |
| B | 从 `postgres:18.1-alpine3.23` 复制 `/usr/local/bin/pg_*` 与 `libpq.so.5` 及其依赖（lz4、zstd、openssl、readline、zlib…） | 小版本与服务端完全一致 | 依赖清单要手工维护，上游镜像改编译选项就会缺库；多架构下更容易出错 |

推荐 A，并在 Dockerfile 里断言主版本：`pg_dump --version | grep -q '^pg_dump (PostgreSQL) 18\.'`。

**CI 版本一致性**（替换 `ci.yml:121` 那条 tools 阶段 grep）：

```bash
pg_major=$(sed -n 's/^x-postgres-image: &postgres-image postgres:\([0-9][0-9]*\)\..*/\1/p' deploy/production/compose.yaml)
test -n "$pg_major"
grep -qF "postgresql${pg_major}-client" deploy/production/Dockerfile
grep -qF "PostgreSQL) ${pg_major}\\." deploy/production/Dockerfile
grep -qF "$(sed -n 's/^x-postgres-image: &postgres-image //p' deploy/production/compose.yaml)" scripts/pg-client-pinned.sh
```

### 3.7 Dockerfile（本任务的改动；多架构与 ldflags 由 versioned-release 在此基础上改）

- 删除 `tools` 阶段（`Dockerfile:13-15`）；`Dockerfile.dockerignore` 删除 `backup.sh`、`restore.sh` 两行。
- app 阶段（`Dockerfile:17-20`）改为：

```dockerfile
# PostgreSQL 客户端主版本必须与 compose.yaml 的数据库镜像一致（CI 检查）。
FROM alpine:3.23@sha256:<实现时用 docker buildx imagetools inspect alpine:3.23 取索引摘要> AS app
RUN apk add --no-cache ca-certificates postgresql18-client \
 && pg_dump --version | grep -q '^pg_dump (PostgreSQL) 18\.' \
 && addgroup -S -g 1000 roomusic && adduser -S -D -H -u 1000 -G roomusic roomusic \
 && mkdir -p /app /data /backups && chown roomusic:roomusic /data /backups && chmod 0700 /backups
```

- `/backups` 在镜像里预先建好并属于 uid 1000：Docker 第一次把**空的命名卷**挂到这个路径时会复制镜像里该目录的属主和权限
  （design-probe 已核实：新卷里 `/backups` 为 `1000:1000 drwx------`，uid 1000 可写）。绑定宿主机目录时不复制，由部署者保证属主（见 config-trim）。
- `HEALTHCHECK` 的 `--start-period` 从 60s 改为 `5m`，加 `--start-interval=5s`：迁移前备份发生在 HTTP 开始监听之前，大库备份可能超过原来的 150 秒宽限，
  `docker compose up --wait` 会把它误判为不健康（Docker Engine 25+ 支持 `--start-interval`，本机 29.1.3）。
- `entrypoint.sh` 不变（`pg_isready` 现在是 18）。

### 3.8 命令行（R9）

`cmd/roomusic/cli.go` 的用法串：

```text
用法：
  roomusic reset-password <用户名>
  roomusic backup
  roomusic restore <备份文件>
  roomusic --version
```

**`roomusic backup`**：`config.LoadBackup()` → `postgres.Connect`（不迁移）→ `Service.RunNow(source=manual)`（阻塞等锁，等待时打印一次“另一份备份正在进行，等它完成…”）。
成功：stdout `已备份：roomusic-20261106-153012-manual.dump（12.3 MB）`，退出 0；失败：stderr `备份没有成功（阶段：pg_dump）。`，退出 1；
用法错误 2；Ctrl-C 130。成功后照常执行保留清理。

**`roomusic restore <备份文件>`**（逻辑在 `internal/backup/restore.go`，终端交互在 `cmd/roomusic/restore_command.go`）：

1. 参数：含 `/` 时当作路径；否则当作备份目录里的文件名（`docker compose run --rm app restore roomusic-…dump` 即可）。必须是可读的普通文件。
2. `pg_restore --list`（stdin = 文件）校验；失败 → “这不是可用的备份文件。” 退出 1。
3. 读备份里的迁移版本：`pg_restore --data-only --schema=public --table=schema_migrations --file=-`（stdin = 文件），解析 `COPY … FROM stdin;` 到 `\.` 之间各行第一列，
   得到最高版本与行数；没有该表 → “无”。不起临时库。
4. 连接目标库（`postgres.Connect`，`application_name=roomusic-restore`）：
   - 当前库迁移版本：`schema_migrations` 存在时 `max(version)`，否则“无”。
   - 权限：`SELECT rolsuper OR rolcreatedb FROM pg_roles WHERE rolname = current_user`，不满足 → “数据库用户没有重建数据库的权限，不能恢复。” 退出 1。
     （生产 compose 里 `roomusic` 是 `POSTGRES_USER`，即超级用户。）
   - 其他连接：`SELECT application_name FROM pg_stat_activity WHERE datname = current_database() AND pid <> pg_backend_pid() AND backend_type = 'client backend'`；
     有任何一行 → “还有 N 个连接在使用数据库，先停止 app：docker compose stop app” 退出 1。app 的连接池由 `postgres.Connect` 设置 `application_name=roomusic`
     （URL 未指定时；`pgx.ParseConfig` + `stdlib.OpenDB`），目录后台维护常驻一条连接，所以正常运行的 app 一定能检测到（A8）。迁移被拒或回退失败进入降级模式的 app 不持有连接，此时恢复对数据无害；
     日志给出的恢复命令先 `docker compose stop app`、最后 `docker compose up -d --wait`，保证 app 重新启动。
5. 打印并确认：

```text
备份文件：roomusic-20261106-030012.dump
备份时间：2026-11-06 03:00（Asia/Shanghai）
大小：12.3 MB
备份里的迁移版本：30
当前数据库的迁移版本：31
恢复会用这份备份覆盖数据库 roomusic，覆盖前会先备份当前数据库。
输入数据库名 roomusic 确认：
```

   备份版本高于本程序支持的最高迁移时多打印一行：“这份备份来自更新的版本，恢复后用对应版本的镜像启动。”。输入不符 → “已取消。” 退出 1。
6. 再查一次其他连接（确认期间 app 可能被启动）。
7. 安全备份：`RunNow(source=pre_restore)`；失败 → “恢复前的备份没有成功，数据库没有改动。” 退出 1。
8. 连接同一服务器的维护库 `postgres`：`DROP DATABASE "<库>" WITH (FORCE)`、`CREATE DATABASE "<库>" OWNER "<当前用户>"`（标识符用 `pgx.Identifier.Sanitize`）。
9. `pg_restore --no-owner --exit-on-error --single-transaction --dbname=<目标库>`，stdin = 备份文件。
10. 成功：打印核对表（`schema_migrations` 最高版本与行数，`users`、`releases`、`tracks`、`favorite_tracks`、`favorite_albums` 行数，表不存在写“缺失”），
    最后一行“已恢复。启动 app：docker compose up -d”，退出 0。
11. 第 8–9 步失败或被中断：

```text
恢复没有成功，数据库现在可能是空的。用刚才的恢复前备份还原：
  docker compose run --rm app restore roomusic-20261106-153512-pre-restore.dump
```

    退出 1。命令在部署目录里可以原样执行（A8）；它会再做一次（对空库的）恢复前备份，无害。

## 4. 前端

### 4.1 路由与导航

- 路径 `/system/backups`：`router.tsx` 新增 `BackupsRoute`（`<AdminGate><BackupsPage /></AdminGate>`），加进 `router.tsx:336` 的 `authenticatedRoute.addChildren`。
  不加 `/system` 跳转（“系统”组目前只有一项）。
- `nav-items.ts:5` 的 `to` 联合类型加 `"/system/backups"`；`nav-items.ts:29` 之后加
  `{ to: "/system/backups", label: "备份", icon: DatabaseBackup, adminOnly: true, group: "系统" }`。
  `nav-items.test.ts:9`、`:14` 的管理员期望各加 `{ label: "系统", items: ["备份"] }`。
  图标 `DatabaseBackup` 来自 lucide-react（待核实 1.41.0 是否导出，见 §8；没有时用 `HardDriveDownload`）。

### 4.2 页面结构

```text
<Page title="备份">
  <BackupStatusSection>          Panel title="状态"，action = 立即备份按钮
    QueryContent(overview) + QueryContent(version)
      StatList：版本 / 最近一次成功 / 下次自动备份
      directory ≠ ready → Notice tone="warning"
      last_failure ≠ null 且 directory = ready → Notice tone="warning"
      POST 失败 → FailureNotice context="write" messages={backupFailureMessages} action=<Button>重试</Button>
  <BackupFilesSection>           Panel title="备份文件"
    QueryContent(overview)
      空 → <p>还没有备份。</p>
      否则 DataTable caption="备份文件"：时间 / 来源 / 大小（numeric）/ 下载
      total > backups.length → <p>只显示最近 500 份。</p>
```

- 按钮：`<Button variant="primary" icon={<DatabaseBackup size={16} />} busy={start.isPending || running}>`，文案 `start.isPending || running ? "正在备份…" : "立即备份"`
  （忙碌态不设 `disabled`，规范 `component-guidelines.md:34`）；`running` 指 `status.running !== null`，定时或迁移前备份进行中同样显示忙碌。
  正在备份时点击不发请求。
- 两个区段共用 `useBackupOverview` 一个查询；版本号用 `useBuildInfo`（`staleTime: Infinity`，进程重启后前端整页刷新才会变）。
- **轮询**：`refetchInterval: (q) => q.state.data?.data.status.running ? 2_000 : false`（`hook-guidelines.md:18`）；页面不可见时 TanStack Query 自动暂停。
  `useStartBackup` 的 `onSettled` 返回 `invalidateQueries(systemKeys.backups(generation))`，让按钮忙碌到看到服务器新状态；回调先查 `scope.isCurrent()`。
- **下载**：直链 `<a href={backupDownloadPath(name)} download>`，`backupDownloadPath` 返回 `` `/api/v1/system/backups/${encodeURIComponent(name)}` ``
  （与封面 `artworkPath` 同理：不是 JSON，不经过 `apiRequest`，同源 Cookie 生效）。链接可见文字“下载”，`VisuallyHidden` 补上“{时间} 的备份”。
- 查询键：`systemKeys.backups(generation) = viewerQueryKey(generation, "system", "backups")`、`systemKeys.version(generation)`。
- 解码器：`d.object({ status: …, backups: d.array(backupFile, { max: 500 }), total: d.integer({ min: 0 }) })`；`source` 用 `d.oneOf([...4 种])`，
  `directory`、`stage` 同理；`version` 用 `d.nullable(d.string({ pattern: /^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$/ }))`，`commit` 用 `d.nullable(d.string({ pattern: /^[0-9a-f]{40}$/ }))`。
- `formatBytes(bytes: number): string` 放 `frontend/src/shared/lib/bytes.ts`：1024 进制，单位 `B KB MB GB TB`，小于 10 保留一位小数、否则取整，`Intl.NumberFormat("zh-CN")`；
  非有限数或负数返回 `UNKNOWN_TIME` 同款占位“—”。

### 4.3 文案草稿（全部）

| 位置 | 文案 |
| --- | --- |
| 导航组 / 项 | 系统 / 备份 |
| 页面标题 | 备份 |
| 区段标题 | 状态；备份文件 |
| 按钮 | 立即备份；忙碌：正在备份…；失败后：重试 |
| StatList 标签 | 版本；最近一次成功；下次自动备份 |
| 版本值 | `0.1.0`；开发构建：开发版 |
| 最近一次成功的空值 | 还没有 |
| 下次自动备份的空值 | — |
| 目录不可用（warning） | 备份目录无法写入，请检查部署设置。 |
| 最近一次失败（warning） | {YYYY-MM-DD HH:mm} 的备份没有成功，可以点“立即备份”再试。 |
| `backup_busy` | 另一份备份正在进行，稍后再试。 |
| 其他写失败 | 沿用 `FailureNotice` 的通用写失败文案 |
| 表格 caption / 列 | 备份文件；时间、来源、大小、下载 |
| 来源标签 | 自动；手动；版本切换前；恢复前 |
| 下载链接 | 下载（读屏：下载 {时间} 的备份） |
| 空列表 | 还没有备份。 |
| 截断 | 只显示最近 500 份。 |

不写：保留天数说明、备份内容范围、权限说明、“备份只包含数据库”之类的解释。

## 5. 兼容与迁移

1. 旧文件：`roomusic-YYYYmmdd-HHMMSS.dump`、`-manual.dump` 原样列出、计入保留清理与“最近一次成功”；`.last-success` 被忽略（不删除）。
   切换当天已有旧定时备份时，新 app 启动不会立刻补备份。
2. 保留清理的匹配从旧的 `roomusic-*.dump`（`backup.sh:51`）收紧为命名正则；不符合的 `roomusic-*.dump` 不再被自动删除（宁可多留）。
3. 文件权限仍是 0600、属主 uid 1000（与旧 backup 服务一致），宿主机上的旧文件可直接被 app 读取与下载。
4. `postgres.Open` 的新参数是可变参数，现有调用方（测试）不用改；`Connect` 新增默认 `application_name=roomusic`，URL 里已有时不覆盖。
5. **开发与测试的 pg 工具**：宿主机没有 pg_dump（2026-11-06 核实 `which pg_dump` 为空），而开发库一旦有新迁移就会触发迁移前备份。新增
   `scripts/pg-client-pinned.sh`：在 `${XDG_CACHE_HOME:-~/.cache}/roomusic/pg-client-<摘要前 12 位>/` 写出 `pg_dump`、`pg_restore` 两个包装脚本并打印目录，
   脚本内容为 `exec docker run --rm -i --network host -e PGPASSWORD -e PGAPPNAME -e PGCONNECT_TIMEOUT <与 compose 相同的 postgres 镜像引用> <工具> "$@"`。
   因为参数里没有文件路径（数据走 stdin/stdout），包装不需要挂载。包装脚本只用 `--network host`（或 Docker 默认 bridge），**绝不创建新的 Docker 网络**
   （本机自动地址池的下一个网段 `192.168.48.0/20` 会覆盖局域网 `192.168.50.0/24`）。`scripts/dev.sh` 导出 `ROOMUSIC_PG_BIN_DIR`，`scripts/test-integration.sh` 导出
   `ROOMUSIC_TEST_PG_BIN_DIR`；取不到时脚本失败（与 `ffmpeg-pinned.sh` 同样不允许静默跳过）。经管道写出的开发备份缺少数据偏移，只是不能 `pg_restore -j`。
6. `scripts/playback-e2e.sh` 启动的真实服务会在启动时尝试补备份；没设置 `ROOMUSIC_PG_BIN_DIR` 时只记一条 `backup.failed`，不影响播放用例。

## 6. 取舍

| 问题 | 选择 | 放弃的方案与理由 |
| --- | --- | --- |
| 状态存哪 | 文件 mtime + 目录内状态文件 | 库表：迁移前与数据库不可用时用不了；继续 `.last-success`：只能存成功时间 |
| 手动备份同步还是异步 | 异步（202/200 + 轮询） | 同步：大库超过反代超时，按钮长时间挂起 |
| 跨进程互斥 | 会话级 advisory lock | 目录锁文件：进程被杀后残留，需要过期判断 |
| 下载 | 直链 + `os.Root` | fetch + blob：大文件占内存，`apiRequest` 只收 JSON |
| 迁移前备份时机 | 迁移事务内、取得迁移锁之后、任何 DDL 之前（§2.2 锁规则） | 事务外预读计划：两个实例同时启动时计划可能已过期 |
| 恢复前安全备份的来源 | 新来源 `pre_restore`（裁决结果 1：采纳） | 记为 `manual`：列表里分不出哪份是恢复前留的 |
| 开发环境的 pg 工具 | docker 包装（`ROOMUSIC_PG_BIN_DIR`） | 非生产跳过迁移前备份：开发与生产行为不一致，测试覆盖不到 |
| pg 客户端来源 | apk `postgresql18-client` | 复制二进制与库（见 §3.6） |

## 7. 运维与回滚

- 日志事件（JSON，`module=backup`）：`backup.started`（source）、`backup.completed`（source、file、bytes、duration_ms）、`backup.failed`（source、stage、exit_code、reason）、
  `backup.retention.deleted`（file）、`backup.retention_failed`、`backup.waiting_for_lock`、`backup.state_unreadable`、`backup.shutdown_timeout`。
  不记录连接串、密码、目录绝对路径以外的环境、pg_dump 原文。
- 停机：生命周期 ctx 取消 → pg_dump 收到 SIGTERM → 删除 partial；`Wait` 在 10 秒停机预算内等待。
- 回滚本任务：本任务落地前生产仍走旧 `make prod`；本任务改的是镜像与代码，切换由 config-trim 的过渡步骤负责，回退步骤也在那里。
  代码层回滚 = 回退提交；备份目录里的新命名文件（`-pre-migration-N`、`-pre-restore`）旧 `backup.sh` 的 `roomusic-*.dump` 清理会照常处理。

## 8. 测试方案

| 验收 | 测试 | 位置 / 命令 |
| --- | --- | --- |
| A1 | 镜像内 `pg_dump --version` 主版本 18（Dockerfile 构建期断言 + CI grep）；compose 只有 app、postgres | `docker run --rm --entrypoint pg_dump <镜像> --version`；`docker compose config --services` |
| A2 | `Plan` 表驱动：到点、当天已备份不重复、启动补备份（无备份 / 25 小时前 / 23 小时前）、失败一小时后重试（含未到计划时刻）、跨日、`Asia/Shanghai` 与 `America/New_York` 夏令时缺口；`Service` 用假 Runner + 假时钟测成功后按天数清理、失败不清理 | `backend/internal/backup/schedule_test.go`、`service_test.go`；`cd backend && go test ./internal/backup/...` |
| A3 | PG 18：产出文件可被 `pg_restore --list` 读取；改错密码让 pg_dump 失败时目录里没有 `.dump` 也没有 partial；两个 goroutine 与两个 Service 实例并发只产出一份；跨进程锁被占时手动返回 `ErrBusy` | `backend/internal/backup/backup_integration_test.go`（`TestPostgreSQLBackup*`），用 `postgrestest` 新增的 `NewDatabase`（建独立数据库，不靠 search_path）；`./scripts/test-integration.sh` |
| A4 | 预置旧命名文件，`Overview` 列出且来源正确，参与清理 | `files_test.go` |
| A5 | 有待执行迁移时先产出 `-pre-migration-<N>.dump` 再迁移；假备份失败时迁移不执行、库不变、`Open` 返回 `ErrPreMigrationBackup`；空库不调用备份；**锁规则探针**：假 `MigrationBackups` 在被调用时另开连接、`SET lock_timeout = '5s'`，对当前 schema 的每张表（含 `schema_migrations`）执行 `LOCK TABLE … IN ACCESS SHARE MODE`，拿不到即测试失败——任何人把 DDL 挪到备份之前都会被它发现；`/readyz` 503 由现有降级 handler 测试与 versioned-release 演练覆盖。缺回退列时的真实 pg_dump 端到端测试归 versioned-release（§4.1 `TestPostgreSQLPreMigrationBackupWithoutRollbackColumns`） | `backend/internal/platform/postgres/migrate_backup_test.go`（`TestPostgreSQLMigrationPreBackup*`，用 `testMigrationFS` 注入迁移） |
| A6 | 非管理员 403、未登录 401、POST 无 Origin / 跨源 403、下载附件头与字节一致、Range、`..%2F..%2Fetc%2Fpasswd`、`roomusic-x.dump`、符号链接、目录同名均 404；样本 | `backend/internal/app/system_api_integration_test.go`、`system_samples_integration_test.go` |
| A7 | Vitest：状态、空列表、正在备份（2 秒轮询、按钮忙碌）、目录不可写提示、失败提示、`backup_busy`、下载链接 href 与可访问名称、开发版；`formatBytes`；导航；mock Playwright 1440 / 375 截图交主会话审阅 | `frontend/src/app/system-backups.test.tsx`、`shared/lib/bytes.test.ts`、`nav-items.test.ts`、`frontend/e2e/system-backups.spec.ts`；`cd frontend && npx vitest run src/app/system-backups.test.tsx src/shared/lib/bytes.test.ts src/app/shell/nav-items.test.ts src/test/api-contract.test.ts`，`npm run test:e2e -- system-backups` |
| A8 | Go：恢复后行数一致；另一连接存在时拒绝；无权限拒绝；人为让 pg_restore 失败时输出的命令可执行。演练：`roomusic-drill` 项目内 `docker compose stop app && docker compose run --rm app restore <文件>` | `backend/internal/backup/restore_integration_test.go`；演练步骤与 versioned-release §8 共用 |
| A9 | `git grep -nE 'backup\.sh|restore\.sh|roomusic-prod-tools|ROOMUSIC_TOOLS_IMAGE|restore-check'` 无结果（归档任务除外） | 收尾检查 |

**本机网络约束**：本机任何测试与演练都不得创建未固定子网的 Docker 网络——自动地址池的下一个网段是 `192.168.48.0/20`，会覆盖局域网 `192.168.50.0/24`。
pg 工具包装脚本只用 `--network host`；A8 的恢复演练在 `roomusic-drill` 项目里进行，网络由 `compose.override.yaml` 固定为 `10.203.51.0/24`。
另请主会话注意：现有 `scripts/test-integration.sh` 用 `compose.test.yaml` 起 `roomusic-test` 项目时，其默认网络 `roomusic-test_default` 也没有固定子网（`compose.test.yaml` 未写 `networks`），
按 2026-11-06 的网络占用会落在 `192.168.48.0/20`；跑本任务的集成测试前需要先处理（例如给 `compose.test.yaml` 固定一个 `10.203.x.0/24` 子网，这不在本任务设计范围内，待主会话决定）。

## 9. 裁决结果（主会话 2026-11-06）

1. **采纳**第四种来源 `pre_restore`：文件名 `-pre-restore.dump`，页面标签“恢复前”（PRD 由主会话同步）。
2. **采纳**迁移前备份文件名带版本号 `-pre-migration-<N>.dump`（N 为迁移前库里的最高版本）。
3. **采纳** docker 包装的 pg 工具（`scripts/pg-client-pinned.sh`、`ROOMUSIC_PG_BIN_DIR`、`scripts/dev.sh` 导出、`scripts/test-integration.sh` 导出 `ROOMUSIC_TEST_PG_BIN_DIR`）；
   包装脚本只用 `--network host` 或默认 bridge，绝不创建新 Docker 网络。
4. **采纳** HEALTHCHECK `--start-period=5m --start-interval=5s`。
5. **采纳**版本接口只给管理员，暂放 `backup/httpapi`。
6. （审阅补充）迁移前备份的锁规则与 `--lock-wait-timeout=60s` 按 §2.2、§3.5 执行。

## 10. 待核实

| 事项 | 核实方法 |
| --- | --- |
| lucide-react 1.41.0 是否导出 `DatabaseBackup` | 主检出 `frontend/node_modules` 里没有 lucide-react；`cd frontend && npm ci && grep -c 'DatabaseBackup' node_modules/lucide-react/dist/lucide-react.d.ts` |
| `pg_restore --data-only --table=schema_migrations` 从 stdin（可 seek 的普通文件）读取自定义格式时只输出该表 | 实现 `restore_integration_test.go` 时直接断言 |
| 管道写出（无数据偏移）的开发备份能被 `--table` 选择性读取 | 同上，用包装脚本产出的文件跑一次 |
| 镜像 apk 安装的 `postgresql18-client` 在 arm64 上同名可用 | `release.yml` 的 `workflow_dispatch` 在原生 `ubuntu-24.04-arm` runner 上构建（裁决：本机不装 binfmt） |
