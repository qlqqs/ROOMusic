# F4 执行计划

> 需求见 [prd.md](prd.md)，设计见 [design.md](design.md)。直接提交到 `main`，不推送。
> 集成测试与冒烟只用 `roomusic-test` compose 项目与合成数据，不碰用户自己的 `roomusic` 项目，也不读写 `music/`。

## 启动前

- [x] 用户批准最终规划摘要后运行 `python3 ./.trellis/scripts/task.py start .trellis/tasks/10-03-classical-f4-artwork-discovery`。
- [x] 读规范：`.trellis/spec/backend/{index,core0-runtime-contracts,quality-guidelines,directory-structure}.md`、
      `.trellis/spec/guides/real-music-assets.md`。

## 步骤

### 第 1 步：代码与测试（R1–R4）

- [x] `catalog/persist.go`：用 `EmbeddedArtwork`、`ArtworkDirectories` 替换 `ArtworkObservation`，并加单元测试。
- [x] `library/scanner.go`：`persistOneCandidate` 直接传候选；`saveArtworkForRelease` 先用内嵌封面，再按目录列表找；
      `discoverFolderArtwork*` 改为多目录、`ReadDir` 加不区分大小写的匹配。
- [x] `library/evidence_test.go`：调整现有三个测试，补上设计里列出的单元用例。
- [x] 集成场景放在新文件 `library/scanner_artwork_integration_test.go`（CUE 含重扫、多碟父目录、父目录与碟目录都有、回退到碟目录、Box 叶子、内嵌优先），
      `scanner_persistence_integration_test.go` 只改了两处调用签名。
- [x] 验证：

  ```bash
  cd backend && gofmt -l . && go vet ./... && go build ./...
  cd backend && go test -race ./internal/catalog/... ./internal/library/... -count=1
  ./scripts/test-integration.sh          # API 样本必须没有变化
  ```

- [ ] 提交：`fix(scan): 封面发现覆盖 CUE、多碟父目录与大小写不同的文件名`。

### 第 2 步：端到端冒烟

- [x] 用 `/tmp/m1b-smoke` 的合成素材，在 `roomusic-test` 项目里起真实后端，扫描后检查：
  - `cue-album` 有封面
  - `double-album` 的封面放回父目录后有封面
  - 其他专辑的封面与 M1b 冒烟时一致

  结果写入 `research/e2e-smoke.md`。结束后清理容器、数据卷与进程，进程只按 PID 结束。

### 第 3 步：文档

- [x] `research/real-library-artwork-layout.md`：真实库只读统计（规划时已写好，只有计数，写明了方法与范围）。
- [x] 运行合同：
  - 封面规则补上查找目录、顺序与大小写
  - 错误矩阵与必需测试同步
  - 技术债两条划掉并注明 F4，补记大小写缺口已修
- [x] 父任务 `09-27-classical-core-1` 的 PRD 任务表加 F4，`implement.md` 加对应条目。
- [x] PRD 补“实施结果与验收对照”。
- [x] 路线文档 `docs/architecture/core-1-roadmap.md` 同步 F4（原文还写着“未修”）。
- [x] 链接检查与 `git diff --check`。
- [ ] 提交：`docs(f4): 封面发现规则与实施结果`。

### 收尾

- [ ] 向用户报告：
  - 实际运行的命令，以及没有运行全量 `go test -race ./...` 的原因
  - 用户重扫真实库后预期的变化
- [ ] 归档：`task.py archive … --skip-branch-validation`，然后修正归档后的相对链接与父任务里的链接。

## 风险与回退点

- `ReadDir` 改变了“不存在”的判定位置：要确认目录不存在时不返回错误，否则所有没有封面的专辑都会变成 `incomplete`。由单元测试“目录不存在时跳过”覆盖。
- 多碟专辑改为先找父目录：两处都有封面的专辑，结果会从单碟封面变为父目录封面。真实库里没有这种情况。
- 回退：revert 第 1 步的提交即可，无迁移。
