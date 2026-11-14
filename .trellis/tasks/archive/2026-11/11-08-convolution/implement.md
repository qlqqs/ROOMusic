# 实施计划：卷积校正

设计：[design.md](design.md)；共用接口：[骨架](../11-09-dsp-batch2-skeleton/design.md)；跨项合入门禁：[父 implement §5](../../../11-08-audio-dsp-reference-port/implement.md)。本文是待审规划，不等于授权启动实现。

后端实现与独立测试均用 `codex/gpt-6-astra`（high），由不同 agent 执行；前端由主会话直接写。工作树建议 `ROOMusic-dsp-convolution`，纯包分支 `dsp/convolution-pure`，接入分支 `dsp/convolution-integration`（SDM只需pure）；测试项目 `roomusic-test-convolution`。不改他人分支/测试库；参考稿只引章节号，本机数据只读。

## 1. 纯包（现在可开工）

1. 取得 SRC 线 FFT 小提交后实现 dsp/convolution：短段 OLA、几何、矩阵路由、延时线、峰位置 Latency。未取得 FFT 时可先写路由/cfg 纯解析，不宣称引擎已可构建验收。
2. cfg/zip 解析可先独立开发；ResampleAll 到位后做重采样/代数和归一。纯引擎无DB/ffmpeg依赖，上传与来源解码不是纯算法步骤。
3. 独立测试写 design §9 的几何、冲激/直接卷积、路由、匹配、归一；长IR短量子测试只验证当前短段 OLA，不另做分区卷积。

## 2. 接入（骨架合入后，按顺序）

1. 等骨架 → SRC → 降混 → PEQ 合 main；0048 建 IR bytea 表，0049 加卷积文档；不使用 ON DELETE SET NULL。
2. dspsettings/irload 与受限 multipart handler：有界临时句柄满足 ffmpeg Source *os.File、请求/解压/解码限额、取消清理、独立进程名额。前端新增 FormData/XHR 上传路径。
3. zones 消费资源端口，plan 与 RegisterDevice 都查本人 IR；DeleteIR 拿用户写锁、按序锁区，在同事务删除资源/清所有引用，提交后同步内存/失效缓存/重建。错误不能统一包成数据库503。
4. render.Options.IRs 与 app 适配；Open 只轻规划固定格式，会话协程加载 IR 再构链/启动进程；修正 encoder=nil 的准备状态，不在播放区锁里做 IO/FFT。
5. Spec.User 的 PEQ/卷积顺序、signal kind、zones settings 和金样；主会话写设置、管理与上传进度，不手改哈希产物。
6. 独立测试补准备期取消/锁不阻塞、资源跨成员、删除后再下区命令、一次超限/清理，以及现有备份还原测试的小 IR 数据。父 §5 两组 e2e 整套门禁后合 main。

## 3. 最小验证集与命令

只验 design 指定的实际改动，不写假想场景或全量兜底。以下 go 命令从 backend 运行；真实渲染测试先在仓库根 `export ROOMUSIC_TEST_FFMPEG="$(scripts/ffmpeg-pinned.sh)"`。本机参考测试若跳过，须单列未完成对照，不能写“指标通过”。

```bash
go test ./internal/dsp/convolution -count=1
# 接入后：
go test ./internal/dspsettings/... ./internal/zones/... ./internal/playback/render -count=1
go test ./internal/app -run '^TestPackageDependencyDirections$' -count=1
```

```bash
ROOMUSIC_TEST_COMPOSE_PROJECT=roomusic-test-convolution \
  ROOMUSIC_TEST_RUN='TestPostgreSQLZones|TestPostgreSQL.*DSP|TestPostgreSQLMigrationRollbackRoundTrip|TestPostgreSQL.*IR|TestPostgreSQL.*Backup' \
  scripts/test-integration.sh
# 有意合同改变后仅更新所属响应，再不带更新变量复跑：
ROOMUSIC_UPDATE_API_SAMPLES=1 ROOMUSIC_TEST_COMPOSE_PROJECT=roomusic-test-convolution \
  ROOMUSIC_TEST_RUN='^TestPostgreSQLZonesAPIResponseSamples$' scripts/test-integration.sh
```

迁移号 **0048、0049**，带 down；新增资源测试沿用 TestPostgreSQL...DSP/IR 命名以纳入范围，实际执行时列出匹配到的用例，不把零匹配/跳过当通过。独立 project 仍受脚本全局锁约束，本机数据库和 real e2e 串行。

```bash
(cd frontend && npx vitest run src/features/player/zones/decoders.test.ts src/features/player/components/signal-path.test.tsx)
(cd frontend && npm run typecheck)
```

新增本项表单/管理/上传交互测试由测试 agent 命名并在交接时追加**精确文件路径**的 vitest 命令；上述既有两文件不能代替新增交互验收。前端主会话在本子项合入前构建一次，产物随本项提交以满足 CI 漂移检查，不由各并行后端分支分别重建。

合入前额外执行父 §5 的整组 real e2e 与 mock e2e，禁止只跑单个新用例代替；日常不跑全仓 go test ./...。运行 `git diff --check`。生成器只在方法/系数变动时运行，CI检查自主产物性质，不重算昂贵优化。

## 4. 回滚点

关卷积可退到已有 PEQ 链；代码回退连同资源端口、删除编排一起撤回。schema 先0049再0048，IR bytea 和选择会删除，先备份；已上传IR不能当成可随意丢弃的缓存。只回退 dspsettings DELETE 而保留 zones 缓存是禁止的半回滚。

## 5. 交接回报

文件与接口变化；真实执行命令/结果/跳过项；最小验收对应证据；迁移升级→回退→再升级结果（有迁移时）；信号路径/界面截图（有界面时）；尚未完成的本机对照与最终iPhone验收。不得把本计划命令列表当作执行记录。各子项实现收尾再更新所属 spec，本次只起草任务文档。

卷积另回报装载路径数、目标率抽头数、IR代数和、Latency和实际短量子行为；有真实cfg zip时仅在本机只读验证，不提交原文件。
