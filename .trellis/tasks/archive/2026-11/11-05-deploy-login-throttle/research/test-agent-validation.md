# 登录限速测试验收记录

日期：2026-11-05。执行范围为 `implement.md` 第 2 步；未提交 Git。

## 结论

- 未发现需要修改实现的缺陷。永久改动只有测试、新增的 429 样本和本记录。
- 16 个新增限速器单元测试通过；9 个新增集成测试通过。
- 隔离 PostgreSQL 回归共有 32 个顶层用例 PASS，没有 SKIP。
- 前端指定 3 个文件通过：480 PASS、1 SKIP。跳过的是既有的“仓外艺人截断队列”测试：未配置其外部大样本目录，与限速无关；两个新增界面测试均实际执行并通过。
- 11 项变异全部被目标断言检出；每次均按原始字节还原，最终 SHA-256 与变异前一致。
- 没有运行全量 Vitest、全量 `-race`、Playwright、dev/prod 脚本；遵守指定的局部验证范围。没有重建前端产物，因为测试改动不影响生产代码。
- 未编辑部署 agent 负责的文件，未还原其他 agent 的改动。

## 文件与验收对应

### `backend/internal/identity/throttle_test.go`

所有时间推进均注入假时钟，无 `sleep`。

| 测试 | 验收 | 关键断言 |
| --- | --- | --- |
| `TestLoginThrottleThresholdBackoffAndRejectedAttemptsDoNotCount` | B1、B6 | 1–4 不锁；第 5 次后锁；等待 1/2/4/8/15/15 分钟；反复拒绝不计数、不延期；纳秒边界 |
| `TestLoginThrottleSuccessClearsOnlyThisCombination` | B2、B1b、B6 | 成功只清本组合；另一来源失败不变；重复成功回调幂等 |
| `TestLoginThrottleSourceIsolationAndExactUsername` | B1b、B4、B6 | 来源独立；大小写与前后空白不折叠 |
| `TestLoginThrottleSourceLimitPauseAndRejectedAttemptsDoNotCount` | B3、B6 | 允许 30，拒绝 31；首次 Started；重复拒绝不加次数/额度，不延长暂停；其他来源独立 |
| `TestLoginThrottleSourceSlidingWindowBoundary` | B3、B6 | `(now−1m, now]`；一分钟前纳秒仍在窗口，满一分钟即过期，61 秒也过期 |
| `TestLoginThrottleSuccessRemovesOnlyItsOwnSameTimeStamp` | B2、B3、B6 | 同时刻多条尝试仅撤回成功那条，不能重复撤额度 |
| `TestLoginThrottleConcurrentAttemptsPrecount` | B1、B6 | 同时启动 16 次，在未完成任何核对时仅允许 5 次，其余 11 次拒绝 |
| `TestLoginThrottleFingerprintResetAndSourcePause` | B5c、B6 | 新指纹立即解除账号锁，但不能解除来源暂停 |
| `TestLoginThrottleForgetsAt24Hours` | B1、B6（T5） | 未满 24 小时保留；最后失败满 24 小时清零 |
| `TestLoginThrottleBoundedAccountsEvictOldestAndSweep` | B6（T5） | 灌满 10000 条仍可记新键，淘汰最旧，过期后清理 |
| `TestLoginThrottleBoundedSourcesEvictOldestAndSweep` | B3、B6（T5） | 灌满 4096 来源后淘汰最旧；窗口过期后清理 |
| `TestLoginThrottleStaleSuccessCannotClearRecreatedState` | B2、B5c、B6 | 指纹变化、遗忘、淘汰三种重建后，旧成功回调不清新状态 |
| `TestLoginThrottlePasswordNamespaceHasNoSourceQuota` | B5b、B6（T9） | 用户 ID 计数，改密码无来源上限；与形似用户名的登录键不碰撞；次数快照稳定 |
| `TestLoginThrottlePasswordSuccessClearsSameFingerprint` | B2、B5b、B6（T9） | 不依赖指纹变化也能在成功后清零 |
| `TestSourceOf` | B1b、B3、B6 | IPv4、IPv6、zone、映射地址、/64 聚合及非法值 |
| `TestLoginThrottleHashesUsernameKey` | B6（T5） | 驻留账号键保存摘要而非原始用户名 |

### `backend/internal/app/login_throttle_integration_test.go`

| 实际 PASS 的新增用例 | 验收 | 关键断言 |
| --- | --- | --- |
| `TestPostgreSQLLoginThrottleLocksAndRecovers` | B1、B1b、B6 | 5 次 401 后正确密码也 429；Retry-After 为 60/1 并向上取整；到点恢复；不写 Cookie/会话；换端口和转发头不能绕过 |
| `TestPostgreSQLLoginThrottleSuccessClearsCombination` | B2、B1b、B6 | 真实成功登录清本组合；另一来源累计失败不被清除 |
| `TestPostgreSQLLoginThrottleMissingAndDisabledUsersMatchWrongPassword` | B4、B6 | 存在但密码错、不存在、停用三者前 5 次 401、第 6 次 429，响应 JSON 完全相同 |
| `TestPostgreSQLLoginThrottleSourceLimitAndRecovery` | B3、B1b、B6 | 30 个不同用户名失败，下一次正确密码也 429；其他来源成功；暂停先于查库；一分钟后恢复 |
| `TestPostgreSQLLoginThrottleDatabaseErrorsDoNotCount` | B6（T10） | 关闭专用业务池，连续 6 次均 503，无 Retry-After；恢复后账号首次计数 1，来源仍有完整 30 额度 |
| `TestPostgreSQLLoginThrottleSessionWriteErrorsDoNotCount` | B2、B6（T10） | PostgreSQL 触发器阻止会话插入，连续 6 次均 503；不占失败来源额度；移除触发器后可登录 |
| `TestPostgreSQLLoginThrottleResetPasswordImmediatelyUnlocks/command` | B5c、B6 | `ResetPasswordByUsername` 模拟命令行，新密码立即登录 |
| `TestPostgreSQLLoginThrottleResetPasswordImmediatelyUnlocks/web` | B5c、B6 | 管理员网页接口重置后立即登录 |
| `TestPostgreSQLPasswordThrottleAcrossSourcesKeepsSession` | B5b、B6（T9） | 当前密码 5 次错仍 403；换来源也 429；用户/会话/操作记录不变；登录不受改密码桶影响；一分钟后成功 |
| `TestPostgreSQLPasswordThrottleSuccessClearsFailures` | B2、B5b、B6（T9） | 两轮“失败 4 次再成功”，当前会话保留 |

重置的两个子用例归属同一个顶层测试，因此新增顶层共 9 个。

### 样本与前端

- `backend/internal/app/api_samples_integration_test.go`：在现有 `TestPostgreSQLAPIResponseSamples` 录制流程中增加专用不存在用户名，录入 `errors/too-many-attempts`（B4、B6）。
- `backend/internal/app/testdata/api-samples/errors/too-many-attempts.json`：唯一新增样本，状态 429、错误码 `too_many_attempts`。更新模式重录其既有管理范围，但 SHA-256 核对确认 465 个原有样本均字节不变；没有其他样本 diff（B6）。
- `frontend/src/app/login-throttle.test.tsx`：429 精确文案“登录失败次数太多，请过几分钟再试。”；留在登录页并保留回跳参数、会话快照/代次与输入；不自动重试，仍可再次提交（B5、B6）。
- `frontend/src/app/account-password.test.tsx`：新增 B5b 用例，429 精确文案“当前密码输错次数太多，请过几分钟再试。”；非字段错误、非结果未知；对话框/输入/会话/缓存/广播保持不变；仍可提交（B5b、B6）。
- `frontend/src/test/api-contract.test.ts` 的登记来自既有未提交实现，本 agent 未改该文件；已验证新样本被完整解码。

## 实际验证命令

以下命令均从仓库根目录执行；最终还原变异后再次执行局部门禁。

```bash
(cd backend && test -z "$(gofmt -l ./internal/identity ./internal/app)")
go -C backend test ./internal/identity/... -count=1
go -C backend vet ./internal/identity/... ./internal/app
(cd frontend && npm run lint && npm run typecheck && npx vitest run src/test/api-contract.test.ts src/app/login-throttle.test.tsx src/app/account-password.test.tsx)
```

结果：全部退出码 0。identity 包最终耗时 0.282 秒；httpapi 无独立测试文件。前端 3 文件通过，480 PASS、1 既有无关 SKIP，最终 10.89 秒。

隔离库创建后执行：

```bash
ROOMUSIC_UPDATE_API_SAMPLES=1 go -C backend test ./internal/app -run '^TestPostgreSQLAPIResponseSamples$' -count=1 -v
go -C backend test ./internal/app -run 'TestPostgreSQL.*(Identity|Login|Throttle|Password|Origin|Session|SetupAdmin|CreateUser|UserRoutes|ListUsers|Unauthenticated)|TestPostgreSQLAPIResponseSamples' -count=1 -v
# 数据库计数变异还原后的定向复测：
go -C backend test ./internal/app -run '^TestPostgreSQLLoginThrottleDatabaseErrorsDoNotCount$' -count=1 -v
```

三条命令均在 `ROOMUSIC_TEST_DATABASE_URL` 指向下述随机测试库时运行。分别 PASS，耗时 20.040 / 71.099 / 1.064 秒，无任何集成 SKIP。筛选里的 `Origin` 也匹配到了两个包含 `Original` 的既有播放用例，因此如实列在下方 PASS 清单；未运行 app 全套。

### 集成顶层 PASS 清单

- `TestPostgreSQLAPIResponseSamples`
- `TestPostgreSQLPermissionMatrixAndImmediateSessionInvalidation`
- `TestPostgreSQLIdentityResponsesCarryUserID`
- `TestPostgreSQLSetupAdminMapsOnlyUniqueViolationsToSetupClosed`
- `TestPostgreSQLCreateUserMapsOnlyUniqueViolationsToUsernameTaken`
- `TestPostgreSQLUserRoutesRejectMalformedAndUnknownIDs`
- `TestPostgreSQLListUsersFailsLoudlyOnUnreadableRows`
- `TestPostgreSQLUnauthenticatedMessageIsRoleNeutral`
- `TestPostgreSQLChangeOwnPasswordKeepsCurrentSessionAndRevokesOthers`
- `TestPostgreSQLChangeOwnPasswordRejectsWrongCurrentPassword`
- `TestPostgreSQLChangeOwnPasswordValidatesNewPassword`
- `TestPostgreSQLChangeOwnPasswordRequiresSessionAndOrigin`
- `TestPostgreSQLChangeOwnPasswordIsAtomic`
- `TestPostgreSQLConcurrentOwnPasswordChangesSucceedOnce`
- `TestPostgreSQLAdminResetPasswordRevokesAllSessions`
- `TestPostgreSQLAdminResetPasswordKeepsDisabledMemberDisabled`
- `TestPostgreSQLAdminResetPasswordRejectsInvalidRequests`
- `TestPostgreSQLAllowedOriginsIdentityLifecycle`
- `TestPostgreSQLAllowedOriginsRejectBeforeIdentityWrites`
- `TestPostgreSQLLoginThrottleLocksAndRecovers`
- `TestPostgreSQLLoginThrottleSuccessClearsCombination`
- `TestPostgreSQLLoginThrottleMissingAndDisabledUsersMatchWrongPassword`
- `TestPostgreSQLLoginThrottleSourceLimitAndRecovery`
- `TestPostgreSQLLoginThrottleDatabaseErrorsDoNotCount`
- `TestPostgreSQLLoginThrottleSessionWriteErrorsDoNotCount`
- `TestPostgreSQLLoginThrottleResetPasswordImmediatelyUnlocks`
- `TestPostgreSQLPasswordThrottleAcrossSourcesKeepsSession`
- `TestPostgreSQLPasswordThrottleSuccessClearsFailures`
- `TestPostgreSQLTrackLyricsRequireLoginAndCheckInputInOrder`
- `TestPostgreSQLPlaybackAudioRequiresLoginAndRevealsNothingWithoutIt`
- `TestPostgreSQLPlaybackAudioStreamsTheOriginalFileWithProtectedHeaders`
- `TestPostgreSQLPlaybackPreflightDescribesTheOriginalRepresentation`

## 变异检查

所有变异均只临时改实现，不改测试断言；每次只运行相关测试，要求因目标断言失败退出 1，然后在 `finally` 中恢复原始字节。

| 变异 | 检出位置 |
| --- | --- |
| 阈值 5 改为 6 | 第 5 次失败没有触发 1 分钟锁 |
| 翻倍改为固定 1 分钟 | 第 6 次应为 2 分钟却为 1 分钟 |
| 上限 15 改为 16 分钟 | 第 9 次应为 15 分钟却为 16 分钟 |
| 账号拒绝分支仍加失败次数 | 下一次失败计数变成 41，而非 6 |
| 来源上限 30 改为 31 | 第 31 次错误放行 |
| 来源暂停期间继续追加时间戳 | 到点仍不能恢复 |
| 成功不删除账号状态 | 下一次尝试仍被锁定 |
| 不比较密码指纹 | 重置后仍被锁定 |
| 查用户数据库错误也调用 Begin 计数 | 来源第 26 个探针就耗尽，应允许完整 30 次 |
| 登录文案映射改成错误文字 | B5 精确文案断言失败 |
| 改密码文案映射改成错误文字 | B5b 精确文案断言失败 |

实际变异命令：

```bash
go -C backend test ./internal/identity/... -run ^TestLoginThrottleThresholdBackoffAndRejectedAttemptsDoNotCount$ -count=1
go -C backend test ./internal/identity/... -run ^TestLoginThrottleThresholdBackoffAndRejectedAttemptsDoNotCount$ -count=1
go -C backend test ./internal/identity/... -run ^TestLoginThrottleThresholdBackoffAndRejectedAttemptsDoNotCount$ -count=1
go -C backend test ./internal/identity/... -run ^TestLoginThrottleThresholdBackoffAndRejectedAttemptsDoNotCount$ -count=1
go -C backend test ./internal/identity/... -run ^TestLoginThrottleSourceLimitPauseAndRejectedAttemptsDoNotCount$ -count=1
go -C backend test ./internal/identity/... -run ^TestLoginThrottleSourceLimitPauseAndRejectedAttemptsDoNotCount$ -count=1
go -C backend test ./internal/identity/... -run ^TestLoginThrottleSuccessClearsOnlyThisCombination$ -count=1
go -C backend test ./internal/identity/... -run ^TestLoginThrottleFingerprintResetAndSourcePause$ -count=1
go -C backend test ./internal/app -run '^TestPostgreSQLLoginThrottleDatabaseErrorsDoNotCount$' -count=1 -v
(cd frontend && npx vitest run src/app/login-throttle.test.tsx)
(cd frontend && npx vitest run src/app/account-password.test.tsx -t B5b)
```

还原核验（最终再次计算）：

| 文件 | SHA-256，与变异前一致 |
| --- | --- |
| `backend/internal/identity/throttle.go` | `b1d1c8d3ac48199e0336638670206a423e49aaf0e3ede2790a71447bc39b3bf2` |
| `backend/internal/identity/login.go` | `60c487bc93f1e9d40e49b90927ca8f063c08fbc965c20f525fdb8ca06ea9534b` |
| `frontend/src/features/auth/components/login-form.tsx` | `8976ac4d6b9856d7ac68c42006083d1236e21592670b51e3808aa0be001782f9` |
| `frontend/src/features/auth/components/change-password-dialog.tsx` | `38b7c9ccac4aac20288ecb67f65d8a95a1ef0f74b6f962c3ed141705c3b90952` |

## 隔离数据库创建与销毁

- Compose 项目：`roomusic-test`。
- 测试库：`throttle_0fe59f613f9b`；用户名/密码都是测试专用的 `roomusic_test`。
- 空闲端口：`127.0.0.1:37965`，通过绑定端口 0 获得。
- 固定子网：`172.31.246.0/24`，创建前检查 Docker 网络与本机路由，没有冲突。
- Compose 文件：`/home/qlqq/workspace/ROOMusic/compose.test.yaml`。
- 临时网络覆盖：`/tmp/roomusic-throttle-validation/network.yaml`，仅设置默认网络 IPAM。
- 明确 `--env-file /dev/null`，没有读取 `.env` 或 `.env.dev`，没有复用业务连接信息。

```bash
export ROOMUSIC_TEST_PG_DATABASE=throttle_0fe59f613f9b
export ROOMUSIC_TEST_PG_PORT=37965
export ROOMUSIC_TEST_PG_USER=roomusic_test
export ROOMUSIC_TEST_PG_PASSWORD=roomusic_test
export ROOMUSIC_TEST_DATABASE_URL='postgres://roomusic_test:roomusic_test@127.0.0.1:37965/throttle_0fe59f613f9b?sslmode=disable'
docker compose --env-file /dev/null -p roomusic-test -f /home/qlqq/workspace/ROOMusic/compose.test.yaml -f /tmp/roomusic-throttle-validation/network.yaml up -d --wait postgres
# 样本、集成、数据库变异与还原复测完成后，EXIT trap 执行：
docker compose --env-file /dev/null -p roomusic-test -f /home/qlqq/workspace/ROOMusic/compose.test.yaml -f /tmp/roomusic-throttle-validation/network.yaml down -v
```

创建日志明确为新建 `roomusic-test-postgres-1`、`roomusic-test_roomusic-test-postgres-data`、`roomusic-test_default`；销毁日志全部 Removed。之后按项目标签列举容器、卷和网络均为空。绝未操作 `roomusic-postgres-1` 或 `roomusic_*` 业务数据卷。

## `rg` 静态复核

```bash
rg -n 'auth/login' backend/internal/app/*test.go frontend/e2e/real/ scripts/playback-e2e.sh
rg -n 'auth/login|Login\(' backend --glob '*test.go'
rg -n 'expectRejectedLogin|signIn\(|login\(' frontend/e2e/real/ --glob '*.ts'
rg -n 'login|test\.real|go run|ROOMUSIC_TEST|compose|account' scripts/playback-e2e.sh
```

- 既有 app 集成每个应用实例独立限速器；密码、初始化、成员、来源用例中同名失败远少于 5，同来源失败远少于 30。当前指定身份回归已经实际 PASS。
- 新样本的 5 次失败使用专用不存在用户名，与管理员/成员桶不同；其余原有登录成功会撤回额度，总来源失败仍低于 30；完整样本回归 PASS。
- 既有命令行恢复集成通常是成功登录；旧密码/去掉首尾空白的密码合计 2 次失败后正确登录，其余停用/回滚场景各仅 1 次失败，不到阈值。这里只静态复核，没有扩大到命令行包集成执行。
- `frontend/e2e/real/account.real.ts` 是两个独立账户用例，各使用随机成员名；每个仅 1 次旧密码拒绝，随即正确密码成功，合计也低于来源上限。
- `frontend/e2e/real/support.ts` 及其他真实服务用例使用已创建的正确凭据；未见批量错误密码重试。
- `scripts/playback-e2e.sh` 本身不发送登录请求；它启动一次性隔离服务后调用真实服务测试。限速状态随新服务清空，成功登录不累计来源失败额度。
- 结论：没有发现现有身份集成/真实服务测试会被意外限速；未修改这些脚本或真实服务测试，未运行 Playwright。

## 证据位置

本机临时日志目录：`/tmp/roomusic-throttle-validation/`；包含 Compose 创建/销毁、样本生成、集成 PASS、数据库变异、8 个单元变异与 2 个前端变异日志。初次前端门禁日志为 `/tmp/roomusic-throttle-frontend.log`。临时日志不作为仓库资产提交，关键结论已保存在本文。
