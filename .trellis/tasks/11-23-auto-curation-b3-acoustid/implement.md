# B3 执行计划

分支：`feat/auto-curation-b3`（从 main 开）。后端实现、后端测试、前端分别由不同的执行者完成（全局约定）；子代理不提交。

## 顺序

1. **后端实现**（Codex `gpt-6.2-astra` high；跨模块：部署、取源抽取、迁移、适配器、闸门、匹配）
   - [ ] 部署：`scripts/fpcalc-pinned.sh`、Dockerfile 的 fpcalc 阶段、`compose.prod.yaml`、`scripts/check-production-compose.sh` 的一致性检查、
         `scripts/test-integration.sh` 的 `ROOMUSIC_TEST_FPCALC`、`.github/workflows/ci.yml` 里取 ffmpeg 处同样取 fpcalc、`config.Server.FPCalcPath`
   - [ ] 迁移 0063（指纹缓存表、源设置的密钥三列）与 down
   - [ ] `playback/fingerprint`（自检、管道、超时）；从分析里抽出共用的取源函数（只抽取）；`catalog` 的缓存读写
   - [ ] `sources`：能力、类型、`ErrInvalidKey`；`sources/acoustid` 适配器
   - [ ] `curation.Sources`：`RequiresKey`、密钥的保存与清除、`key` 字段、`inactive_reason` 新值、`FingerprintFinder`、密钥状态回写；`curation/httpapi` 两个接口
   - [ ] `program`：`Fingerprints` 接口、`FingerprintStep`、专辑与单曲的新一步、`acoustid_mismatch`/`acoustid_spread`/`title_mismatch`、`Candidate.Score`、`fingerprint` 结果字段、`needsWork` 的新条件
   - [ ] `app`：装配（`fingerprint.Open` 失败只让 AcoustID 不可用；同一个 `secret.Store`；`WithCurationSources` 增加 AcoustID 的选项给受控服务）
   - [ ] 自检：`gofmt`、`go vet ./...`、`go build ./...`、改动包的现有单元测试；不写新测试
2. **后端测试**（Codex `gpt-6.2-sol` high；另一个代理）
   - [ ] 适配器：受控 HTTP 服务（gzip 表单、参数只有那五个、间隔、429/503 退避、码 4/17 → `ErrInvalidKey` 不缓存、码 3 → 空、映射与去重、缓存命中不出站）
   - [ ] 规则表：专辑第 6 步每条通过与不通过（抽样不足、spread、too_many、mismatch、title_artist_mismatch、ambiguous、通过）；单曲（ambiguous、duration、title_mismatch、占位标题通过）；
         `fp=nil` 时的提前返回与 `fingerprint` 字段三种值；`ErrInvalidKey` 与途中 `source_disabled` 时往下走到搜索
   - [ ] `needsWork`：没有字段/`unavailable`/`used`/`not_reached` × 各状态 × 可用与否
   - [ ] 源状态：原因顺序、`key` 字段只出现在 AcoustID、密钥接口（403、400 两种、404、只返回末四位、库里只有密文、清除）
   - [ ] 集成（PostgreSQL + 真实 ffmpeg + fpcalc）：整轨与 CUE 虚拟轨算指纹；第二次不启动进程（用计数的包装或缓存行时间戳证明）；内容指纹或 CUE 帧变化后重算；
         fpcalc 缺失时 `fingerprint_unavailable` 且其余源与匹配正常；受控 MusicBrainz + AcoustID 跑一轮：占位标签的专辑经 `acoustid` 对上、拿不准建待定项、
         密钥无效 → `key_invalid` 且发行仍走到搜索；升级场景（预置 B3 之前形状的状态）没有密钥时零重做、填密钥后只重做该重做的
   - [ ] 接口样本：外部源列表（新增 AcoustID 一项）、设置密钥的响应；`ROOMUSIC_UPDATE_API_SAMPLES=1` 生成，其余样本不变
3. **前端**（主代理）：解码器与端点、AcoustID 行与密钥对话框、确认框文案、`method="acoustid"` 的依据文案、测试数据与假接口、Vitest、Playwright 冒烟、截图复核
4. **部署验证**（主代理）：构建生产镜像（amd64），`docker run --rm --entrypoint /usr/local/bin/fpcalc <镜像> -version`，与改动前镜像比较大小，
   `scripts/check-production-compose.sh`，用独立 Compose 项目名启动一次看健康检查（只清理本任务创建的容器与网络）
5. **文档与规范**：design 第六节的表；父任务 implement.md 的交接；路线图
6. **合入**：审阅越界改动，本地提交、合并到 main、删分支，归档任务

## 审阅后的补充（design 第八节）

- 后端实现按第八节的包划分、接口与选项；`CurationEvidence`/`CurationTrack` 不加字段；`Match`、`DecideAlbum`、`WithCurationSources` 保持签名。
- 新的集成测试名用 `TestPostgreSQL…` 或 `TestFFmpeg…` 前缀；`ROOMUSIC_TEST_FPCALC` 为空时失败。
- 后端测试还要覆盖：摘要在 B3 前后对同一证据不变（固定一份基准哈希）；暂停与扫描时不写 `unavailable`；密钥 compare-and-set；回写不改 `updated_at`；
  `acoustid_sparse`；曲目数或碟数不符的版本被筛掉；单曲经 `RecordingByID` 读回；暂时性失败 → `try_later`；缓存失败的重试规则。
- 文档还要改：ADR 0006 两处差异的决策记录与原文标注；ADR 0005、`sources-contracts.md` 第 9 节写明发给 AcoustID 的还有应用密钥；部署合同的许可与源码地址。

## 验证命令（范围上限；按实际改动收窄）

```bash
cd backend && mise exec -- gofmt -l . && mise exec -- go vet ./... && mise exec -- go build ./...
cd backend && mise exec -- go test ./internal/sources/... ./internal/curation/... ./internal/playback/fingerprint/... ./internal/platform/config/...
sg docker -c 'ROOMUSIC_TEST_RUN="<新测试名的正则>" mise exec -- ./scripts/test-integration.sh'
sg docker -c 'ROOMUSIC_TEST_RUN="<涉及的样本测试>" ROOMUSIC_UPDATE_API_SAMPLES=1 mise exec -- ./scripts/test-integration.sh'
cd frontend && mise exec -- npx vitest run src/features/curation src/test/api-contract.test.ts && npm run -s typecheck && mise exec -- npx eslint src e2e
cd frontend && LD_LIBRARY_PATH=… NO_PROXY=… mise exec -- npx playwright test e2e/curation.spec.ts
sg docker -c 'docker build -f deploy/production/Dockerfile -t roomusic:b3-check .'
./scripts/check-production-compose.sh
```

音频分析的现有集成测试只在取源函数被抽取后跑一次（证明行为不变）。

## 回退点

- 迁移 0063 有 down：删表与三列。
- 部署改动独立于功能：去掉 fpcalc 阶段后 AcoustID 显示 `fingerprint_unavailable`，其余照常。
- 匹配改动只在 AcoustID 可用时生效；`fp=nil` 的路径与 C 完全相同（规则表测试固定）。
