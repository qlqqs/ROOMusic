# 自动整理 B3 补充：内置 AcoustID 应用密钥

## 目标

按 AcoustID 推荐的做法接入：项目自己注册一个 AcoustID 应用，发布镜像内置这个应用的密钥（`client`），每个实例的管理员只填自己的用户密钥（`user`）。管理员不必再去注册应用，开启 AcoustID 从两步变成一步。

## 决定（qlqq 2026-11-23，“AcoustID 按照推荐的做法来”）

- 修订 ADR 0006 U8 与“不采用”表里的“仓库内置共用的 AcoustID 密钥”：改为发布镜像内置项目注册的应用密钥。应用密钥标识的是“ROOMusic 这个应用”，用户密钥标识每个实例的管理员；AcoustID 按用户密钥计量，Picard 等客户端也是这样做的。
- 应用密钥不进 Git：发布流水线从仓库 secret `ACOUSTID_CLIENT_KEY` 经 BuildKit secret 注入 Go 链接参数。它会出现在二进制里（与其他客户端一样不可避免），但不出现在镜像历史、构建参数或仓库中。
- 自己从源码构建、没有注入密钥的镜像，保持现在的行为：管理员填应用密钥与用户密钥两把。

## 需求

1. 后端 `internal/sources/acoustid`：
   - 包级变量 `bundledClientKey`，由 `-ldflags -X` 注入；为空表示没有内置。
   - `Declaration().Keys`：有内置时 `["user"]`，否则 `["application","user"]`。其余字段不变。
   - 出站表单：有内置时 `client` 用内置密钥，不读也不要求管理员的应用密钥；否则维持现状。
   - 内置密钥被 AcoustID 拒绝（错误码 4、17）时管理员无法修复：客户端在本进程内记下“内置密钥被拒”，只在第一次记一条 error 级日志 `sources.acoustid.bundled_key_rejected`（不含密钥），本次照旧返回 `InvalidKeyError{application}`。下游不用改：`guardedFingerprint` 只标 `Declaration.Keys` 里的名字，`application` 不在其中，不会把用户密钥标成无效；指纹这一步遇到 `ErrInvalidKey` 记为 `unavailable`，文字匹配照常走。客户端导出 `BundledKeyRejected() bool`。
   - `curation.SourceRegistration.Available func() bool` 改为 `Unavailable func() string`（返回不生效原因，空串表示可用）。`app.go` 里 AcoustID 返回：没有指纹工具时 `fingerprint_unavailable`；内置密钥被拒时新原因 `bundled_key_rejected`。源随之不生效，`fingerprintStep` 返回 nil，`needsWork` 不再重开（不会循环）；换上新镜像重启后标记消失，记着 `fingerprint="unavailable"` 的发行按现有规则重开。
   - 有内置时，库里早先存的 `application` 一行保留不动、不读不显示；`DELETE …/keys/application` 返回 404。换回没有内置的构建时它照旧生效。写进合同，不做迁移。
2. 构建：
   - `deploy/production/Dockerfile` 的 Go 构建 `RUN` 加 `--mount=type=secret,id=acoustid_client`（默认可选）：文件非空（`-s`，没设的 GitHub secret 传来的是空值）才读入，内容只许字母数字（否则构建失败），再加 `-X github.com/qlqq/roomusic/backend/internal/sources/acoustid.bundledClientKey=<内容>`。没有 secret 时结果与现在相同。镜像历史只记未展开的命令，不含密钥。注释写明 secret 内容不参与构建缓存键：本地先无后有地构建要 `--no-cache`。
   - `bundledClientKey` 是未初始化的包级 `var string`（`-X` 只对这种变量生效）。
   - `.github/workflows/release.yml` 的 build-push 步骤传 `secrets: acoustid_client=${{ secrets.ACOUSTID_CLIENT_KEY }}`；`publish == 'true'` 而 secret 为空时，在构建前失败并说明原因（以免悄悄发出要两把密钥的镜像）。`ci.yml` 与冒烟脚本不传，不受影响。
3. 前端：`SOURCE_INACTIVE_REASONS` 加 `bundled_key_rejected`，文案“ROOMusic 内置的应用密钥被 AcoustID 拒绝了，升级到新版本后恢复。”；替身的判断顺序同步。现有界面已按后端返回的密钥逐行显示，只有一把用户密钥时只显示一行，不改结构。确认框“发给外部源的内容”里 AcoustID 一条现为“……以及你填入的应用密钥与用户密钥”，有内置时不准确；改为两种情况都成立的“……以及应用密钥与你填入的用户密钥”。
4. 文档：ADR 0006 新增决定行（qlqq 2026-11-23）并在 U8、第 8 节源表、发出的数据、“不采用”表处注明修订；ADR 0005 第 67、148 行；`core-1-roadmap.md` 第 119 行；前端 `interface-layout-guidelines.md`、`state-management.md` 的密钥说明；代码注释 `curation-sources-section.tsx`、`api/decoders.ts`、`source-key-row.tsx`；`go version -m` 能看到链接参数里的密钥（反正在二进制里），在部署合同里写一句；`sources-contracts.md` §8a、`curation-contracts.md` §16 的密钥部分、`deployment-environment-contracts.md` 与 `core0-runtime-contracts.md` 发布条目写明 secret 注入与无 secret 时的行为。

## 验收

- `go test ./internal/sources/acoustid/...`（测试内设置包变量，在 `New()` 之前设、`t.Cleanup` 复原、不并行）：有内置时声明只有 `user`、表单 `client` 为内置值且不读管理员的应用密钥；内置被拒（4、17）返回 `InvalidKeyError{application}`、`BundledKeyRejected()` 变真、日志只一条；没有内置时原有用例不变。
- curation 单元测试用合成声明（`Keys=["user"]`）：设置项只列一把，填上后不再 `key_missing`；查询被拒于 `application` 时不把 `user` 标成无效；`Unavailable` 返回 `bundled_key_rejected` 时源不生效、原因如实。原有 `fingerprint_unavailable` 用例照过。
- 受影响的 B3 集成用例（`Available` 改名涉及的）通过；API 样例由不带密钥的测试构建生成，保持两把，不需重生成。
- `docker build --no-cache` 不带 secret 成功；带合成 secret（`test-client-key` 之类，只经 `--secret env=`/临时文件传入）构建后二进制含该值。合成值不进仓库文件。
- 前端 typecheck、eslint、curation 设置的 Vitest 通过（含只有用户密钥一行、`bundled_key_rejected` 文案）。
- 本机与文档里不出现真实的应用密钥。

## 不做

- 不让管理员在有内置时另填应用密钥覆盖（真有需要再加）。
- 不在本机注册应用或设置 GitHub secret：需要 qlqq 在 https://acoustid.org/new-application 注册应用并执行 `gh secret set ACOUSTID_CLIENT_KEY`（对外操作，交付时提示）。
- 不推送，不打标签。

## 规划决定方式

子代理审阅路线（qlqq 已授权子任务规划完直接实现）。规划子代理（Plan）审阅：两处必须改已采纳（内置被拒不走退避而是本进程内停用、只记一次日志）；采纳的建议：Dockerfile 用 `-s` 与字符检查、缓存键注意、发布缺 secret 时失败、旧 `application` 行写进合同、补齐文档位置、包变量测试的复原与顺序。

## 验证记录（2026-11-23）

- `go build ./...`、三包 `go vet`、`go test ./internal/sources/acoustid/...`：通过（实现代理与测试代理各跑一次；首跑一次 `TestLookupFingerprintGlobalInterval` 测得 333.99ms 偶发低于阈值，复跑通过，与本改动无关）。
- 集成：`TestPostgreSQLB3`（8 个，含新增 `TestPostgreSQLB3AcoustIDBundledKeyGate`）通过；`TestPostgreSQLCurationSources*`、`TestPostgreSQLCurationProgramSourceSwitches` 通过（`sourceState` 改写后的回归）。
- 镜像：不带 secret `--no-cache` 构建通过；带合成 secret（尾部换行）构建，二进制含该值、`docker history` 不含，换行已去掉。检查镜像与合成文件已删除。
- 前端：typecheck、eslint、`curation-settings.test.tsx` 与 `api-contract.test.ts`（725 个，1 个跳过）通过。
- 未做：arm64 构建；真实 AcoustID 应用密钥（需 qlqq 注册并设置仓库 secret）。
