# Classical M2a：鉴权直出与真实播放

## 目标与用户价值

让已登录用户从现有资料库发起真实音频播放：原文件经鉴权直出，跨页面连续播放，能操作队列，
并能看懂当前浏览器为什么不能播放某首曲目。播放状态只由真实媒体事件驱动，不宣称硬件 Bit-perfect。
技术设计见 [design.md](design.md)，执行计划见 [implement.md](implement.md)。

## 依据

- `docs/architecture/core-1-roadmap.md` 阶段表 M2a 行：鉴权直出串流、播放引擎、浏览器能力判定，不可播放的曲目明确标注；
  验收出口为“约定浏览器/格式可播放、拖动/取消/错误可恢复、切页不断播，不宣称硬件 Bit-perfect”。
- `docs/architecture/adr/0004-media-delivery.md`：D1（2026-09-28 已批准，M2a 只直出已在目标浏览器验证可播放的容器与 codec，
  放不了的曲目明确标注，例如 Chrome/Firefox 下的 ALAC 与需要精确裁剪的 CUE 虚拟轨；派生缓存由 M2b 承接）；
  §3 边界、§4 能力协商、§5 Range 与鉴权；§9 要求 M2a 规划时先批准实播矩阵与撤销规则；§11 待批准决策。
- `docs/architecture/adr/0001-frontend-foundations.md:18`：页面兼容目标 Chrome/Edge 111+、Firefox 113+、Safari 与 iOS Safari 16.4+，
  自动化只跑 Chromium。页面目标不是音频实播矩阵，收窄媒体验收不等于取消页面兼容目标。
- `frontend/DESIGN.md` §4、§6（`frontend/DESIGN.md:163`、`:255`）与 `.trellis/spec/frontend/player-design-guidelines.md:31` 起的交互合同：播放栏布局、队列与模式语义、信号路径。
- `.trellis/tasks/09-27-classical-core-1/prd.md` 的 R4：能播放约定格式和 CUE 曲目、操作队列并切页不断播；信号路径只写可验证事实（由 M2a、M2b、P 分担）。
- 调研：[research/library-formats.md](research/library-formats.md)（真实库格式构成与浏览器解码预期）、
  [research/automation-feasibility.md](research/automation-feasibility.md)（无头 Chromium 真实播放验证、Firefox/WebKit 可行性）。

## 已核实事实（2026-10-03）

### 后端

- 路由只装配身份、资料库、catalog（`backend/internal/app/app.go:37`–`39`），没有读取音频字节的接口；
  `backend/internal/catalog/projection.go:27`–`42` 的曲目事实不能当作媒体直出能力。
- 每个受保护请求由 `Guard.Authenticate`（`backend/internal/identity/guard.go:55`）查询会话与用户（未撤销、未过期、用户未停用）。
  会话 Cookie 为 HttpOnly、SameSite=Lax，`<audio>` 的同源请求自动携带，不需要 URL token。
- 封面读取的顺序是 401 → 404 → 304 → 读文件，`private, no-cache` 加强 ETag（`backend/internal/catalog/httpapi/handlers.go:200`）。
- 曲目投影有 `source_kind`、`codec`、`bit_depth`、`sample_rate`、`channels`、`bitrate`、`duration_seconds` 与 `cue`，没有容器或 MIME 字段。
  `source_kind` 取值为 `flac_vorbis_comment`、`m4a`、`mp3_id3v2`、`wav_info`、`ogg_vorbis_comment`、`opus_tags`、`cue_virtual`；M4A 区分 `alac`/`aac`。
- 物理曲目合并 CUE 证据后 `cue` 也非空（`backend/internal/catalog/read.go:279`），判断需要切分的虚拟轨只能看 `source_kind == "cue_virtual"`。
  一个物理文件只被一条 CUE 曲目引用时保留物理曲目，被多条引用（整轨镜像）时保留虚拟轨（`backend/internal/catalog/organizer.go:940`）。
- tracks 表有 `source_root_id`、`relative_path`、`source_status`，以及迁移 0013 的 `source_size`、`source_modified_at`，可在播放时核对文件是否在扫描后变化。
- 目录停用只把 `library_roots.status` 设为 `disabled`：扫描跳过、已入库内容保留且仍可浏览（`releaseVisiblePredicate` 只看
  `source_status='present'`，`backend/internal/catalog/read.go:28`）；界面文案在 `frontend/src/features/library/components/disable-root-dialog.tsx:50`
  与 `root-command-notice.tsx:23`。
- HTTP 服务只设 `ReadHeaderTimeout`（`backend/cmd/roomusic/main.go:51`–`55`），长时间输出不会被写超时切断；关停时 `Shutdown` 最多等 10 秒。
  日志中间件的写入包装保留 `ReadFrom`、`Flush`。Go 1.25 可用 `os.Root` 在根目录内打开文件。
- 新包必须先在 `backend/internal/app/dependency_rules_test.go` 声明依赖规则；跨能力只经公开合同，各 httpapi 包互不导入。

### 前端

- 会话结束的统一清理在 `frontend/src/app/runtime.ts:44`（`session.onEnded`）；已登录子树以会话代次为 key 重新挂载
  （`frontend/src/app/shell/authenticated-layout.tsx:13`）。目前没有播放器生命周期，也没有使用 `localStorage`/`sessionStorage`。
- `AppShell` 只有侧栏与主内容；导航没有“正在播放”（`frontend/src/app/shell/nav-items.ts`）。专辑卡片不放播放按钮是 M1b 的裁决。
- 前端规范要求浏览器持久数据带版本、边界校验和账号命名空间，损坏、未知版本、存储不可用都能恢复（`.trellis/spec/frontend/state-management.md:227`–`229`）。
- `frontend/playwright.config.ts:22` 只配置 Chromium，Playwright 用路由模拟、不经过 Go 服务；仓库里没有任何音频测试素材。

### 真实库与浏览器

- 只读聚合统计：284 个音频文件，FLAC 199（16/44.1 占 164）、M4A/ALAC 84（24/48 占 73）、MP3 1；全部双声道，最高 48 kHz；
  没有 AAC、WAV、Ogg、Opus。FLAC 几乎都有 SEEKTABLE，ALAC 的 `moov` 都在文件开头。
- 8 个 CUE 都是“每轨一个文件”的多 FILE 形式，引用的 FLAC 会并入物理曲目；真实库目前大概率没有需要切分的虚拟轨（确切数字以扫描为准）。
- MDN：FLAC 在 Chrome、Edge、Firefox 51+、Safari 11+ 可解码，ALAC 只有 Safari 可解码。本机无头 Chromium 153 实测：FLAC 16/44.1 与 24/48
  真实解码、按真实时间推进、拖动触发新的 Range 请求、`ended` 来自媒体；ALAC 的 `canPlayType` 为空。浏览器首个请求是 `bytes=0-` 的长连接。
- 推论：只做直出时，FLAC 与 MP3（约 70%）预期在四种桌面浏览器可播；ALAC（约 30%）预期只有 macOS Safari 可播，其他浏览器在 M2b 之前标注不可播放。

## 关键决策

### 用户裁决

| 编号 | 决策 | 选择者与日期 | 结果 |
| --- | --- | --- | --- |
| K0 | 任务与使用场景 | qlqq，2026-10-03（Codex 会话） | 同意在 Core 1 下创建 M2a 并规划；Windows、macOS、Linux 都有浏览器使用场景，规划不得只围绕单一系统 |
| K1 | 播放器交付范围 | qlqq，2026-10-03（选推荐项“完整播放器”） | 底部播放栏；专辑页的整张播放、从某首开始、下一首播放、加入队列；队列（移除、上移下移、清空，全部可用键盘）；顺序、列表循环、单曲循环、随机四种模式；正在播放页（封面、信号路径、接下来）；刷新后以暂停状态恢复队列，退出登录即清除；系统媒体键。父任务 R4 的“操作队列”在 M2a 交付，M2b 只加派生与歌词 |
| K2 | 实播矩阵与验证方式（ADR 0004 第 1 项） | qlqq，2026-10-03（选推荐项“自动化加三组手工”；取代 Codex 会话提出、未获答复的 8 组手工方案） | 每次改动由 Linux 无头 Chromium 驱动真实 Go 服务与合成样本自动验证；验收前用户手工实播三组：macOS Safari、Windows 上常用的 Chrome 或 Edge、任一系统上的 Firefox，记录系统与浏览器版本；其余组合与 iOS/Android 不作门禁；ADR 0001 的“自动化只跑 Chromium”不变。格式：FLAC 16/44.1 与 24/48、ALAC 16/44.1 与 24/48、MP3 必须实测；WAV、AAC、Ogg Vorbis、Opus 只在自动化中用合成样本验证，通过才批准直出 |
| K3 | 撤销与停用（ADR 0004 第 5 项中 M2a 需要的部分） | qlqq，2026-10-03（选推荐项“10 秒复核，停用即停播”） | 每个新请求重新鉴权；已建立的传输每 10 秒复核会话、成员、目录与曲目，不满足或数据库不可用即切断；停用目录 = 扫描跳过 + 不能播放，专辑仍可浏览，停用文案同步修改；浏览器已缓存的音频无法收回，合同写明 |

M2b 依赖 M2a 的接口与播放引擎，且须先裁决 ADR 0004 第 2–7 项（含 FFmpeg 分发与许可），在 M2a 规划获批后单独立项（2026-10-03 用户要求“规划 M2”时的处理）。

### 随规划批准的技术决策

| 编号 | 决策 | 理由 |
| --- | --- | --- |
| T1 | 新能力包 `playback` 与 `playback/httpapi`；三个接口：`GET /api/v1/playback/formats`、`GET /api/v1/tracks/{id}/playback`（预检）、`GET\|HEAD /api/v1/tracks/{id}/audio` | 媒体元素读不到错误正文，预检能在出声前给出原因并在失败后分类（ADR 0004 §5）；格式表只有一份，由后端下发并据此拒绝 |
| T2 | 严格版本核对：文件大小、修改时间须与扫描记录一致，容器标识须与格式一致，否则 `409 source_changed` 并提示重扫 | 不用旧曲目信息播放已被替换的内容；代价是改标签后要重扫才能播 |
| T3 | 安全打开：复核根目录仍被配置允许，`os.Root` 加 `O_NONBLOCK` 打开后对句柄检查普通文件 | 满足 ADR 0004 §3 的 containment、链接逃逸、FIFO 与替换竞态要求，且打开不会阻塞 |
| T4 | 刷新恢复用 `sessionStorage`，按账号命名，只存曲目、发行与组的标识及模式、位置；恢复时重新读取发行详情 | 只承诺刷新恢复；多个标签页互不覆盖；不把私有服务器数据留在浏览器持久层 |
| T5 | 预检或解码失败的条目标为失败、显示原因并按模式前进；网络错误或 503 停在暂停并可重试；`401` 结束会话 | 失败有明确状态，不静默跳过，也不在暂时性故障时连续跳歌 |
| T6 | 导航按原型分为“聆听”（概览、专辑、正在播放）与“资料库”（扫描目录、成员）；队列为空时不显示播放栏 | 新增正在播放页需要分组；DESIGN 要求无播放器时不留空行 |
| T7 | 测试素材用固定版本的 FFmpeg 容器一次性生成并提交（合计不超过 1 MB）；新增真实服务自动化脚本与 CI 作业 | K2 要求每次改动都能自动验证；FFmpeg 只用于生成素材，不进入运行时、部署或 CI |

## 需求与验收

| 编号 | 需求 | 验收 | 可观察结果 |
| --- | --- | --- | --- |
| R1 | 按当前权限读取原文件音频 | A1 | 未登录或会话失效时音频与预检都返回 401，不含长度、`Content-Range` 或格式；只用同源 Cookie，无 URL token；响应不含宿主机路径 |
| | | A2 | 200 全量、206 单范围与后缀范围、`If-Range`、HEAD、多范围与越界的 416 JSON 行为正确；响应头为 `private, no-store`、`nosniff`、强 ETag、无 `Last-Modified` |
| | | A3 | 文件缺失、链接逃逸、FIFO、根目录不再被允许时返回 `source_unavailable`，不阻塞、不越界；源文件只读 |
| | | A4 | 大小、修改时间或容器标识与记录不符时返回 `source_changed`，界面提示重新扫描 |
| | | A6 | 新请求立即拒绝；传输中撤销会话、停用成员或停用目录后，连接在复核间隔内被切断（测试用短间隔）；关停时中止；停用目录返回 `root_disabled`；停用文案已修改 |
| R2 | 真实播放与连续生命周期 | A7 | 预检后播放；状态、进度、时长只来自媒体事件；失败按 T5 处理；自动播放被拒时停在暂停；拖动只在时长有效且可定位时启用 |
| | | A8 | 立即播放、下一首播放、加入队列、移除、移动、清空的语义符合 DESIGN §6；四种模式符合 K1；队列上限 10,000 条 |
| | | A9 | 站内切页不断播；退出、会话失效、换用户时停止并清空；另一标签页退出后本页同步停止 |
| | | A10 | 刷新后以暂停状态恢复队列与位置，失效条目丢弃并提示数量；会话结束删除保存的队列；损坏、未知版本、存储不可用时可恢复 |
| | | A12 | 系统媒体键可播放、暂停、切歌、定位，媒体信息为当前曲目 |
| R3 | 诚实的能力与质量表达 | A5 | 格式表只含已验证组合；CUE 虚拟轨、不在格式表的格式、浏览器不能解码的曲目在专辑页禁用并写明原因；整张播放跳过它们并提示数量 |
| | | A11 | 播放栏、正在播放页、导航与专辑页动作可用；信号路径分源文件、服务端、浏览器、系统输出四段，系统输出写“未知”，不出现“Bit-perfect”；键盘可完成核心操作；五种宽度无横向溢出且播放栏不遮挡内容；axe 通过；长标题不破坏布局 |
| R4 | 可复核的兼容性验收 | A13 | 新接口样本与 decoder 测试通过，样本无漂移；依赖规则包含新包 |
| | | A14 | 真实服务自动化脚本在本机与 CI 通过，覆盖 design §7 列出的场景 |
| | | A15 | 三组手工实播记录完整（系统与浏览器版本、格式参数、播放与拖动结果）；ALAC 在 Safari 实播通过才保留在格式表 |
| | | A16 | 新增播放合同规范；ADR 0004 决策记录、DESIGN、前后端规范、路线图与父任务同步；`.trellis/spec` 下没有超过 24,000 字节的文件 |

## 风险与延期

- Safari 的 Range 请求方式、类型检查与自动播放策略只能靠手工实播发现，问题在执行计划第 8 步修正。
- 复核上界不含浏览器缓冲；合同与界面写明“已缓存的音频无法收回”。
- T2 会让改过标签但未重扫的文件暂时不能播放；如影响使用再议放宽。
- 素材生成需要一次性拉取 FFmpeg 镜像；失败时只用代码生成 FLAC/WAV，并把其余格式移出格式表。
- 24 bit WAV 经 FFmpeg 写出是 WAVE_FORMAT_EXTENSIBLE，扫描记为 `wav_extensible`，不在格式表中，因此 M2a 只批准 16 bit PCM WAV 直出
  （2026-10-05 生成素材时发现；真实库没有 WAV）。让扫描器按子格式把它识别为 PCM 并经浏览器验证，留给后续任务。
- 多个标签页各自独立播放，不做跨标签页协调；单个音频元素，不预加载下一首，不承诺无缝衔接。
- `library-scan-contracts.md` 只剩约 900 字节余量，本任务只在其中加一行链接。
- 附带发现（留给 M2b 的 CUE 语义）：EAC“间隙附加到前一轨”的多 FILE 写法中，切换 FILE 后的 `INDEX 01` 会被解析器丢弃，见 research/library-formats.md。

## 范围外

- FFmpeg、转码或重封装、派生缓存、CUE 精确分轨、歌词（M2b）。
- 服务端播放历史（P）；不记录演示或虚假的播放历史。
- 写入音乐源文件、无缝播放、预加载、跨标签页播放协调、硬件直通或 Bit-perfect 承诺。
- iOS/Android 的媒体实播门禁（页面兼容目标保持不变）；专辑卡片上的播放按钮。

## 前置依赖

M1a 外壳与会话生命周期、M1b 专辑详情、F3 的封面缓存头与 F4 的封面发现均已交付。不需要迁移。

## 最终规划决策记录

- 路径：人工选择；选择者：qlqq。
- 已选结果：K0–K3 均由用户在 2026-10-03 选择（K1–K3 为推荐项）；T1–T7 随最终规划摘要一并提交批准。
- 理由：先定播放器范围、实播矩阵和撤销语义，接口与测试才能定稿（ADR 0004 §9）。
- 批准（2026-10-05）：用户在询问“FLAC 与 ALAC 由前端还是后端解码”并得到说明后批准最终规划摘要（“批准执行”）。
  说明的结论：M2a 后端不解码，只直出原文件字节，由浏览器内置解码器解码；ALAC 只有 Safari 能解，其余桌面浏览器标注不可播放
  （真实库约 30%），要让它们可播需 M2b 的无损派生（后端 FFmpeg 转 FLAC）。用户选择保持现有范围，不把 M2b 转码提前，也不做前端 WASM 解码。
- 状态：已批准，可运行 `task.py start` 并按 `implement.md` 实施；M2b、P、M3 仍各自单独规划。

## 实施结果与验收对照

状态：**已完成，2026-10-07 归档**。第 1–3 步此前已提交；第 4–7 步 2026-10-05 补齐，经第二轮审查修复后于 2026-10-06 分组提交。
用户要求“直接帮我做决定并完成任务收尾”，收尾方式由 Claude 代为决定（见下方“收尾决策”）：ALAC 没有实播证据，移出直出格式表；
A15 三组手工实播与 A14 的远端 CI 移交[后续验收任务](../10-06-m2a-acceptance-followup/prd.md)。下表为收尾时的证据与边界。

| 验收 | 当前证据与边界 |
| --- | --- |
| A1–A4、A6 | 既有后端实现与接口/安全打开/慢速连接测试；本轮隔离 PostgreSQL 上播放与 API 样本最小集成 `-race` 通过，样本未漂移；未匿名泄露资源 |
| A5 | FLAC 两组参数、MP3、16 bit WAV、AAC、Vorbis、Opus 七个素材由真实 Chromium 解码通过；ALAC 没有实播证据，2026-10-06 移出格式表（`13a9cfe`），与 CUE 虚拟轨、24 bit WAV-ext 一样禁用且不请求音频；Safari 通过后在后续任务恢复 |
| A7、A8、A10、A12 | 引擎/纯队列/能力/恢复/媒体键回归通过；短素材真实时间、seek、自然结束与暂停恢复通过；系统媒体键的实际 OS 行为仍列入手工表 |
| A9 | 路由切换连续与跨标签退出后停播、清空恢复记录真实专项通过；会话结束同步清理，不混同普通卸载 |
| A11 | MenuButton/底栏/正在播放/队列/不可播放原因组件回归通过，五宽与 axe 专项7项、全页面Playwright158项通过；375/1280长标题实际截图复验通过，保留完整可访问名称 |
| A13 | 注册并解码两项成功/六项错误播放样本，全样本覆盖断言通过；后端依赖方向测试通过 |
| A14 | 本机真实 Go + PostgreSQL + Chromium 13 项通过，包括从干净导出的提交树用 `npm ci` 全新安装后运行（Node 24.16.0、Go 1.25.10，与 CI 相同）；新增 CI 作业，未推送，远端 CI 未运行，移交后续任务，不写成 CI 已通过 |
| A15 | **移交后续任务**：需要 macOS Safari、Windows Chrome 或 Edge、任一系统 Firefox 的实机，由用户执行；[手工验收表](../10-06-m2a-acceptance-followup/research/manual-acceptance.md)已移到该任务 |
| A16 | 已同步播放合同、ADR、设计与前后端规范；路线/父任务已记录实施结果与移交项，规范大小/链接检查通过；ALAC 移出已同步合同、ADR、DESIGN 与素材说明 |

### 已执行的验证

- 引擎定向：`cd frontend && npx vitest run src/features/player src/test/api-contract.test.ts src/shared/lib/duration.test.ts src/features/catalog/format.test.ts`。
- 全前端：`cd frontend && npm run lint && npm run typecheck && npm test`；52 文件、938 测试通过。
- 路由模拟浏览器：`cd frontend && npx playwright test --workers=2 --output=/tmp/roomusic-player-full-check-results`（`LD_LIBRARY_PATH` 指向本机 Chromium 依赖目录，复用本轮临时 preview），158 项通过；
  `npx playwright test e2e/player.spec.ts --workers=2 --output=/tmp/roomusic-player-check-results`，7 项通过。最终 UI 小范围16项组件回归通过。
- 本机真实：`LD_LIBRARY_PATH=/tmp/chromelibs/root/usr/lib/x86_64-linux-gnu ./scripts/playback-e2e.sh -g 'seek|合成源'`，2 项通过；
  相同环境 `./scripts/playback-e2e.sh`，13 项通过。容器/卷/临时目录与服务精确清理，无测试资源残留。
- 后端最小单元：`cd backend && go test -race ./internal/playback/... ./internal/app/... -run 'Test(CheckRange|Rechecking|Format|FindFormat|Fixtures|PackageDependency)' -count=1`。
- 后端最小集成：隔离 `roomusic-test` PostgreSQL，`cd backend && go test -race ./internal/playback/... ./internal/app/... -run 'TestPostgreSQL(Playback|APIResponseSamples)' -count=1`。
- 生产构建：`cd frontend && npm run build -- --config <临时envDir:false配置>`；临时配置仅展开既有 Vite 配置并禁止读取 .env；连续两次内嵌产物119文件字节相同。
- 内嵌后端：`cd backend && go build ./... && go test ./cmd/roomusic -count=1`，构建与资产合同测试通过。
- `bash -n scripts/playback-e2e.sh`、`cd frontend && npm exec eslint -- e2e/real playwright.real.config.ts`、`npm exec tsc -- --noEmit`；脚本与真实配置通过。
- `git diff --check`、Markdown 相对链接检查、`find .trellis/spec -name '*.md' -size +24000c`、`task.py validate`：通过；
  DESIGN 超注入上限的警告保留，新增落地补充末尾可能被截断；manifest 已注明不能假定全文件注入，
  当前完整可执行合同另收于小型播放器/状态/播放规范文件。

未运行全量后端测试：后端业务源码本轮没有变化，最小播放/样本集成覆盖相关合同；
外壳影响全部前端页面，按获批执行计划运行全量前端，非默认扩大所有项目测试。
修复与证据边界详见[独立检查记录](research/implementation-review.md)。

### 第二轮审查修复（2026-10-05）

Claude 复核后修复 12 项问题（后端无缺陷），明细见[审查修复记录](research/review-fixes.md)。上方 938 项与 158 项计数早于这次修复。修复后实际执行：

- `cd frontend && npm run lint && npm run typecheck`：通过。
- `npx vitest run src/features/player src/test/api-contract.test.ts src/shared/lib/duration.test.ts src/shared/ui/menu-button.test.tsx src/features/catalog src/app/album-detail.test.tsx src/app/library-roots.test.tsx src/app/shell src/features/library/components/root-command-notice.test.tsx`：22 文件、494 测试通过；
  新增的 11 条回归测试先在还原旧行为的代码上确认失败。
- `npm run build -- --config <临时envDir:false配置>`，再构建到临时目录比对：119 个文件字节相同；`cd backend && go build ./... && go test ./cmd/roomusic -count=1` 通过。
- `npx playwright test e2e/player.spec.ts e2e/albums.spec.ts e2e/keyboard.spec.ts e2e/a11y.spec.ts --workers=2 --output=/tmp/<临时目录>`：106 项通过。
- `LD_LIBRARY_PATH=/tmp/chromelibs/root/usr/lib/x86_64-linux-gnu ./scripts/playback-e2e.sh`：13 项通过（37.9s），测试容器与卷已清理。

两项 e2e 早于最后一处修复：在队首按“上一首”时作废待定位的位置（`previous()` 中一行）。
两套用例都不经过“上一首”，这处由引擎回归测试覆盖；修复后重跑的是 lint、类型检查、上面的 Vitest、内嵌资产重建与比对和 `go test`。

未运行前端全量与其余 52 项路由模拟用例：改动集中在播放器、专辑页与外壳订阅，相关测试集已覆盖；第 5 步提交门禁时按计划重跑全量。
后端源码未改动，未运行后端测试。

### 收尾决策（2026-10-06）

用户要求“直接帮我做决定并完成任务收尾”。以下由 Claude 代为决定：

| 事项 | 决定 | 理由 |
| --- | --- | --- |
| 提交分组 | 按执行计划第 4–7 步各一个提交，第二轮审查修复按 implement.md 写好的分组并入；每组先把暂存区导出成完整文件树，在其上验证后再提交 | 计划已获批；导出暂存区能验证每个提交本身可构建、测试通过，也不需要 amend |
| A15 三组手工实播 | 移交后续验收任务 | 需要 macOS 与 Windows 实机，本机无法执行，不以 Linux 自动化冒充 |
| ALAC | 移出直出格式表（`13a9cfe`），Safari 实播通过后恢复 | ADR 0004 D1 只直出已验证可播放的组合，A15 规定 Safari 通过才保留，而 ALAC 在任何浏览器上都还没有实播证据；Chromium、Firefox 本来就不能解码，只有 Safari 暂时受影响 |
| A14 远端 CI | 不推送，移交后续验收任务；本机改用干净提交树加 `npm ci` 验证 | 用户一贯只提交到 `main` 不推送（本地领先 `origin/main` 72 个提交）；仓库公开，推送不可撤回 |

### 收尾验证（2026-10-06）

每组都在导出的暂存树（`git checkout-index -a`）上验证后再提交：

- 第 4 步：`npm run lint && npm run typecheck`；`npx vitest run src/features/player src/test/api-contract.test.ts src/features/catalog`，14 文件、389 测试通过；
  用 `envDir:false` 临时配置构建，与已提交的内嵌产物 119 个文件字节相同（引擎尚未接入页面），所以该提交不含资产改动。
- 第 5 步：`npm run lint && npm run typecheck && npm test`，52 文件、954 测试通过；构建结果与暂存的内嵌产物字节相同；
  `LD_LIBRARY_PATH=… npm run test:e2e -- --workers=2`，158 项通过，webServer 用默认配置构建的产物也相同；提交后 `git diff --exit-code -- backend/cmd/roomusic/web` 为空。
- 第 6 步：`bash -n scripts/*.sh`；`docker compose --env-file /dev/null -f compose.test.yaml config --quiet`；CI YAML 可解析；
  `npm ci` 后 `npm run typecheck`、`npm exec eslint -- e2e/real playwright.real.config.ts`；`LD_LIBRARY_PATH=… ./scripts/playback-e2e.sh`，13 项通过（38.0s）。
- 第 7 步：`git diff --cached --check`、规范大小、相对链接检查、`task.py validate` 通过。
- ALAC 调整：`gofmt -l`、`go vet ./...`、`go build ./...`；`go test -race ./internal/playback/... ./internal/library/parser/... ./internal/app/... -count=1`；
  隔离 `roomusic-test` PostgreSQL 上 `go test -race ./internal/playback/... ./internal/app/... -run 'TestPostgreSQL(Playback|APIResponseSamples)' -count=1`，18 个集成测试通过，样本无漂移；
  `npx vitest run src/test/api-contract.test.ts src/features/player`，10 文件、290 测试通过；真实服务 13 项通过。测试容器与数据卷均已删除。

未运行后端全量（`go test ./...` 与全部 `TestPostgreSQL`）：后端只改了播放格式表与对应测试，已覆盖 playback、parser、app 包与播放、样本集成。
远端 CI 未运行（未推送）。

### 收尾边界

第 4–7 步与 ALAC 调整已分组提交到 `main`（`d1c976d`、`06071f8`、`1096443`、`4dc1889`、`13a9cfe`），未推送。
三组手工实播、Safari 上恢复 ALAC 与远端 CI 见[后续验收任务](../10-06-m2a-acceptance-followup/prd.md)。没有读写真实 `music/`。
