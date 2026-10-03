# ADR 0001：前端基础依赖、状态边界与同源交付

## 状态与适用范围

- 状态：**已批准并由 M1a 实施**：第 8 节第 1–3 项与接口防漂移做法于 2026-09-28 批准，第 4、5 项于 2026-10-01 随 M1a 规划批准，
  第 6 项（后端行为调整）按独立事项处理，不属于本 ADR 的批准范围；其中封面与静态资源的缓存头已由 F3 单独批准并实施（2026-10-02，见决策记录）。
- 仓库与外部资料核验日期：**2026-09-27**（第 1 节的证据是 M0 时点的快照）；2026-10-01 的依赖版本核对见 M1a 技术设计。
- 所属交付：M0 的 R4 文档草案；批准内容由 M1a 脚手架实施，M1b 沿用。
- 实施状态：M1a 已在 2026-10-01 安装依赖并锁定版本、重建 `frontend/src/`、恢复真实构建与资产漂移检查，并以单元、页面级、
  浏览器与真实接口冒烟验证（证据见 M1a 任务的 `design.md`、`implement.md` 与 `research/real-api-smoke.md`）。
- 本文不定义新业务 API、最终 DTO 或 SQL；现有 REST 与安全合同继续生效。

## 决策记录

| 日期 | 选择者 | 已批准 | 仍待批准 | 依据 |
| --- | --- | --- | --- | --- |
| 2026-10-02 | qlqq | 第 8 节第 6 项中的缓存头作为独立任务 F3 批准并实施：(1) 封面 `Cache-Control: private, no-cache` 加 `ETag: "<资源 ID>"`，每次显示前带凭据重新验证，未变返回 304 且不读文件，退出登录后验证得到 401；(2) `assets/` 下带内容哈希的文件 `public, max-age=31536000, immutable`，入口页、SPA 回落与其他文件 `no-cache`，目录路径不再列目录；(3) 所有 JSON 响应（含错误信封）`no-store` | 无；第 8 节第 6 项的其他后端行为调整仍须独立列项 | 任务 `10-02-classical-f3-backend-gaps` 的 PRD，裁决 D5、D6 （用户 2026-10-02 批准最终规划摘要） |
| 2026-10-01 | qlqq | 第 8 节第 4、5 项随 M1a 规划批准：(4) 查看者会话代次、统一清理、跨标签页广播与重新可见时核对作为 M1a 的验收门禁，API 请求一律 `no-store`，封面的 HTTP 缓存头属于后端行为，M1a 不改；(5) 直接依赖精确固定（版本见 M1a 技术设计），支持 2023 年以后的常青浏览器（Chrome/Edge 111+、Firefox 113+、Safari 与 iOS Safari 16.4+，构建目标据此设置），自动化只跑 Chromium，查询只对网络失败与 503 重试且最多 2 次、4xx 与无效响应不重试、写操作从不自动重试、窗口重新聚焦时刷新已过期数据（保鲜期 30 秒）。另批准两项对 2026-09-28 记录的细化：字体改用 Fontsource **可变字体**包（只有 woff2，一个文件覆盖 400 与 600，实测产物中 112 个 woff2 合计约 6.0 MB），静态包会连同 woff 产出约 22 MB 而不采用；测试工具为 Testing Library、Playwright 加 axe-core（CI 只装 Chromium），不引入 MSW、jest-dom 等其他测试库。接口样本放在 `backend/internal/app/testdata/api-samples/`，漂移由集成作业把关 | 第 8 节第 6 项（后端行为调整，另行立项） | 任务 `09-28-classical-m1a-shell-admin` 的 PRD 关键决策 K1–K4、K7 |
| 2026-09-28 | qlqq | 第 8 节第 1–3 项按第 3 节推荐整套采纳：TanStack Router + TanStack Query；Classical 全局变量 + CSS Modules；用 Fontsource 自托管 Cormorant Garamond、Lora、Noto Serif SC 的 400 与 600 字重，保留 `unicode-range` 分片与系统字体回退，字体许可在 M1a 逐项复核。另批准接口防漂移做法：Go 集成测试把真实响应写成 JSON 样本，前端 decoder 测试读取这些样本，样本变更未提交时 CI 失败；OpenAPI 留到 M6 设计工具 schema 时再评估 | 第 8 节第 4–6 项（会话代次与跨标签页失效、依赖精确版本与浏览器矩阵、重试策略；后端行为调整另行立项） | 任务 `09-28-core-1-roadmap-revision` 的 PRD，裁决 D3 |

## 1. 当前证据

以下是 2026-09-27（M0）的证据快照；其中“`frontend/src/` 已删除”“依赖仍是 `latest`”“CI 区分真实入口构建与占位检查”等前端现状已被 M1a
（2026-10-01）改变，见“决策记录”。

| 仓库证据 | 可以确认的现状 | 不能据此推断 |
| --- | --- | --- |
| `frontend/package.json`、`package-lock.json`、`tsconfig.json` | 保留 React、React DOM、TypeScript、Vite、ESLint、Vitest 等声明和锁文件；TypeScript 开启 `strict`；多项直接依赖声明仍是 `latest` | 没有已批准的新依赖版本组合；`latest` 声明不等于每次 `npm ci` 都会自动升级 |
| `frontend/index.html` 与缺失的 `frontend/src/` | HTML 仍引用 `/src/main.tsx`，但源码目录已删除；不能把历史 decoder、播放器或 feature 目录描述成当前可运行实现 | 不能靠旧规范认定前端已经完成拆分或具备播放能力 |
| `frontend/vite.config.ts`、`vite.config.dev.ts`、`backend/cmd/roomusic/main.go`（嵌入）、`backend/internal/platform/httpserver/frontend.go`（提供） | 生产产物目标为 `backend/cmd/roomusic/web`，由 Go 嵌入并同源提供；开发配置代理 `/api`，有受限 Host 配置 | 开发代理不是生产网关，也不取代后端 Origin 校验 |
| `.github/workflows/ci.yml`、`backend/cmd/roomusic/web/index.html` | CI 区分真实入口构建与缺失入口占位检查；当前嵌入页说明前端重设计中 | 占位检查通过不是 M1 UI 验收 |
| `backend/internal/app/app.go`、`backend/internal/identity/guard.go` | 已有 setup/auth/users/roots/scans/releases/artworks 路由；会话采用 HttpOnly Cookie，后端检查撤销、过期和用户禁用；写操作校验 Origin | 客户端角色、路由守卫或缓存均不具有授权权威 |
| `frontend-desgin/_ds/classical-d3de10a0-5b67-4da7-b5b3-8d49f26a63f1/styles.css`、`frontend-desgin/ROOMusic 原型.dc.html` | 原型有 Classical CSS 变量；使用 Cormorant Garamond、Lora，页面补充 Noto Serif SC，并通过 Google Fonts 加载 | 原型的远程字体、全局类和运行时不自动成为生产依赖 |

当前锁文件未包含 TanStack Router、TanStack Query 或 Fontsource。旧规范中的 `requestApi`
和 decoder 是需要承接的边界约束，不是可以直接引用的现存源码。

## 2. 要解决的问题

恢复前端时需要同时解决可分享 URL、异步服务器状态、会话切换、密集型 Classical 界面与
可重复交付。若全部放回一个入口组件，路由、请求、权限呈现和样式会再次耦合；若同时引入
多套缓存和 UI 框架，则会增加状态重复和迁移成本。

自然归属保持不变：URL 拥有可分享筛选，后端拥有资料库和操作状态，组件拥有表单与短期交互，
身份边界拥有会话生命周期。任何新库都必须服务这些归属，而不能重新定义它们。

## 3. 推荐方案与可行替代

下表的路由、服务器状态、样式和字体四行已于 2026-09-28 按推荐批准（见“决策记录”），
其余仍是推荐；批准的是选型，不是已安装的依赖清单，精确版本在 M1a 锁定。

| 主题 | 推荐 | 可行替代与取舍 |
| --- | --- | --- |
| 基础工具链 | 延续 React + 严格 TypeScript + Vite，不因重设计引入 SSR 或另一套服务端框架 | 继续此工具链但完全采用原生路由/请求；无需为采用 Router/Query 更换 React 或构建器。React 官方也区分基础构建工具与额外的路由、数据获取需求，不能把 Vite 当完整应用框架（F01） |
| 路由 | M1 评审采用 TanStack Router，负责路径、搜索参数解码、导航和页面错误边界；先选能覆盖实际页面的最小组织方式 | 原生 History API + `URLSearchParams` + feature 级状态协调可交付有限页面；代价是自行维护 `popstate`、深链、参数归一化、加载和焦点行为。不能让两套路由同时拥有同一个 URL |
| 服务器状态 | M1 评审采用 TanStack Query，统一缓存身份、失效、取消、状态查询和轮询；Router loader 如需预取，委托同一 Query 所有者 | 原生 `fetch` + `AbortController` + feature hooks 可行；必须自行实现竞态与会话隔离。也可仅采用 Router 而暂不采用 Query，但须明确唯一的数据缓存 owner，不把 Router 缓存与手写缓存长期双写（F02–F05） |
| 样式 | 从原型提炼少量全局设计变量、基础排版与重置；组件布局和状态优先使用 CSS Modules | 有命名空间的普通 CSS 同样可行，适合极少组件；跨 feature 全局选择器会增加回归面。Vite 已支持 `.module.css`，无需另选运行时样式库（F06） |
| 字体 | 评审后自托管所需字重/字集，可选 Fontsource 构建期包；任何情况下保留系统字体 fallback | 系统字体优先是低成本、无字体下载的可行首发方案，但字宽与视觉一致性不同。原型的远程字体不建议成为生产必需网络依赖（F07–F09） |
| 数据验证 | 延续 `unknown` → 单一 feature decoder → typed DTO → view model；优先窄小、可测试的手写 decoder | 后续若重复校验达到实际维护瓶颈，可单独评审运行时校验库；TypeScript 或路由类型推断本身不能替代响应校验 |

### 路由与 Query 的协作边界

TanStack Router 的官方文档给出与外部数据缓存协作的方式；这证明可以分工，不代表项目已经
采用集成适配器（F02）。具体 API 和适配器包在实施时按锁定版本核验，不在 M0 复制会漂移的
初始化样板。

- Router 拥有经过校验的 URL 参数、导航和页面进入/退出；不复制一份可独立修改的 Release。
- Query 拥有服务器响应缓存和重新验证；query key 包含资源、搜索、筛选、分页以及查看者作用域。
  官方要求 key 唯一描述数据，查询函数依赖的变量应纳入 key（F03）。
- loader 只委托同一 feature 查询，不再以另一个 `fetch` 形成第二份权威。预取、陈旧时间、重试、
  焦点重新获取和扫描轮询需要逐类配置，不能未经评审继承默认值。
- 管理写操作优先悲观更新；按现有 API 携带 revision/幂等信息。409 保留草稿并提供重新核对，
  请求“被接受”不等于任务“成功”。无幂等保证的写请求不能盲目重试。
- 不把服务器结果同步复制进通用全局 store；表单、弹窗、选择状态就近管理。

### Classical 变量与字体

视觉来源仍是原始设计资产，落地锚点为 M0 建立的 `frontend/DESIGN.md`。后续提炼只复制
已核对的变量与语义，不修改或直接打包设计工具运行时；CSS Modules 不能代替设计变量治理。

字体的推荐顺序是：先保留可用系统 fallback，再评估自托管的真实视觉收益。Fontsource 提供
自托管安装、字集和 `font-display` 配置资料；字形覆盖取决于实际字体，不能仅凭“自托管”
推定简体中文完整（F07–F09）。

1. 先核对拉丁与中文的实际覆盖、400/600 等所需字重及生成文件大小，比较可变与静态字体；
   优先保留包自带的 `unicode-range` 按需加载，不无差别引入全部字重，也不盲目手工裁切字集（F07、F08）。
2. 对 Noto Serif SC 评估分片与 `unicode-range`，验证中文、人名、数字及中英混排；不能仅因
   包名正确就声称缺字问题已解决。必要时回退到目标平台系统字体，具体字体栈由视觉验收确定。
3. 建议显式选择 `font-display: swap`，同时验证替换前后的字宽、换行和布局位移；不能保证零位移。
   不为消除字体闪动而长期隐藏正文，也不无差别预加载所有中文分片（F08、F09）。
4. 字体随同源静态构建交付，不在运行时代理任意字体 URL。上游字体分别附有 OFL 文本；实际
   包版本的字体、许可证、版权声明及修改/再分发条件须逐项复核，不能用 Fontsource 包名代替
   字体许可审查（F10）。M0 没有下载字体或完成发行合规验收。

## 4. 边界数据流与会话隔离

建议的数据流（不是目录已存在或最终接口签名的声明）：

```text
浏览器 URL → Router 的搜索参数边界 → 页面 / feature hook
                                    ↓
                             feature 查询或命令
                                    ↓
                 共享 HTTP 传输（同源 Cookie、取消、错误分类）
                                    ↓
             现有 REST → unknown → feature decoder → typed DTO
                                    ↓
                    当前身份作用域的 Query → view model → 组件
```

共享传输只拥有凭据发送方式、通用错误 envelope、安全错误信息和关联标识；feature 拥有
端点合同、枚举、nullable 语义、数值/列表上限与 decoder。未知状态、损坏 JSON 或 HTML 误响应
必须安全失败，不能用断言把它们送入缓存。URL、浏览器持久化数据也各自需要一次入口解码。

### 身份切换是独立的清理边界

推荐为每次已确认的会话建立不含凭据的“查看者 ID + 本地会话代次”。同一用户重新登录也换代，
不能只在 query key 里放用户 ID 就认为隔离完成。

1. 启动时从现有 `GET /api/v1/auth/me` 确认会话；不能从本地角色快照直接恢复私有页面。
2. 任意受保护请求确认 401 时统一进入身份失效流程；403 仅撤销对应操作/资源的呈现，不冒充注销。
3. 注销、会话失效或切换账号时，先封闭旧身份的新请求与渲染，递增代次，停止轮询和预取。
   把取消信号传到真正的 `fetch`；TanStack Query 的取消信号必须被消费，组件卸载本身不保证
   请求停止或不再缓存结果；官方还列出 Suspense hooks 的取消限制，若选用必须另验（F04）。
4. 清理该身份的 Query 查询与 mutation 缓存、Router loader 数据和私有组件状态；再创建新身份的
   数据边界。QueryClient 的清理方法只处理它自己连接的缓存，不能替代 Router/组件清理（F05）。
5. 所有查询、写操作回调及 session bootstrap 结果在提交 UI/缓存前检查发起代次。旧请求即使
   未能物理取消，也不得给新账号写入数据、错误、通知或导航。登录/注销转换应串行协调。
6. 取消 HTTP 等待或删除 mutation 缓存不等于后端撤销写操作；结果不明时按既有操作合同重新查询，
   不显示“已撤销”。409、503 等不能被吞成假成功。
7. M2 的播放器还须停止音频、清空媒体来源、队列、预加载和对象 URL；M1 不预建假播放器。
   不自动迁移旧演示收藏/历史到真实用户。若未来持久化偏好，须有版本、身份隔离和解码。
8. 多标签页只传播不含 Cookie/token 的身份失效提示，并在恢复活动时重新核对身份；广播不构成
   服务端授权。撤销发生但客户端尚未获知时，不能宣称本地立即清空；后端继续鉴权每个请求。

凭据只留在浏览器 Cookie 与后端会话中，不进入 URL、localStorage、IndexedDB、Query key
或日志。私有服务器数据不默认加入持久化 Query 缓存、Service Worker 或离线存储。

Query 清理也不等于清除浏览器 HTTP 缓存：不能宣称仅靠 QueryClient 就阻止了所有旧图片回显。封面原先是 `private, max-age=86400`，
退出登录后一天内仍可能从同一浏览器的 HTTP 缓存直接读到。F3（2026-10-02）改为 `private, no-cache` 加 `ETag`：浏览器可以保存封面，
但每次显示前必须带凭据重新验证，未变得到 304，退出登录后得到 401，旧响应不再使用。选重新验证而不是 `no-store`，因为封面的资源 ID
就是内容哈希、验证只是一次查库，而 `no-store` 每次都要整张重新下载，也不能追溯擦除先前存下的数据（F12）。F3 之前已被浏览器保存的
旧响应在其一天的新鲜期内仍可能被直接使用，随后自然过期。浏览器端的缓存行为由响应头合同与集成测试保证（Playwright 不经过 Go 服务）。
[ADR 0004](0004-media-delivery.md)另行约束未来音频缓存。

## 5. 依赖锁定与同源交付

- M1 若批准依赖调整，先核对 `.mise.toml`、现有 lock 和各包的 peer/engine 要求，选出一个
  可验证的兼容集合，再把直接依赖声明改为审阅过的明确版本并一起提交 lock。本文没有推荐
  “最新版本”，官方文档中的 `latest` 路径也不是版本锁定。
- 记录引入每个包的用途、许可证及维护/升级方式。确认版本后使用仓库既有 `npm ci` 做干净安装；
  npm 官方说明它要求 manifest 与 lock 一致、不会改写 lock（F11）。可重复安装不等于永久安全。
- TypeScript 检查保持独立；Vite 的 TypeScript 转换不能替代类型检查，既有 `tsc --noEmit && vite build`
  应保留其职责（F06）。不因接入第三方类型而关闭严格检查。
- 生产继续将唯一的前端产物嵌入 Go，与 `/api/v1` 同源，无独立 Node 生产服务，也不改用前端可读
  token。开发代理与 Host allowlist 继续使用专属开发配置。
- 恢复源码后，M1 必须恢复实际构建和资产一致性验收，不能只走入口缺失分支。浏览器深链刷新、
  资源 404、API 错误必须分别验证。
- 当前未匹配路由可能落入 SPA 回退。若需为 `/api/` 增加明确 JSON 404，另列后端行为变更及测试，
  不把它夹入 B0 的行为保持拆包，也不让 decoder 将 HTML 当业务结果。

## 6. 阶段、兼容与回退

| 阶段 | 允许推进的内容 | 保持的边界 |
| --- | --- | --- |
| M0 | 比较、记录证据和待决定项 | 不改 manifest、lock、代码、CI、字体或原型 |
| M1，批准后 | 恢复外壳、边界解码与现有 API 页面，实施获批的最小路由/请求方案（M1a 于 2026-10-01 完成；M1b 于 2026-10-03 沿用，没有新增依赖） | 包含已有 setup、登录、成员、目录、扫描等管理操作；不新增整理、播放或伪造数据 |
| B0 | 独立拆分后端职责 | 不以“配合 Router”为理由改变 API、鉴权或错误语义；M1 不等待全部拆包完成 |
| M2 及以后 | 在既有身份生命周期上接真实 PlayerEngine、个人数据等 | 新能力有独立授权与验收；不得复活旧模拟时钟作为真实播放 |

若 Router/Query 的集成成本或兼容性不满足门禁，可选择表中的原生方案；feature API 与 decoder
边界保持不变，替换单一状态所有者，而非长期并存两套请求体系。路由切换要保留获批的 URL 语义，
必要时明确规范化/重定向规则。字体加载或体积不达标可回退系统字体，不回退到必须联网的原型。

M0 文档可独立撤回；未来依赖变更以成对的 manifest/lock 和对应源码提交回退，并重新生成匹配的
生产资产，不能只降某个包或混用旧资产。此方案不引入业务数据库迁移。

## 7. 后续验收案例（尚未执行）

| 类型 | 场景 | 必须观察到的结果 |
| --- | --- | --- |
| 成功 | 登录后搜索、分页、打开详情、前进/后退及深链刷新 | URL 与页面一致；一次 feature 解码；无平行的服务器状态副本 |
| 成功 | 管理员启动/取消扫描，操作完成后刷新页面 | 状态来自后端；终态停止轮询；幂等/revision 沿用现有合同 |
| 失败 | 后端返回损坏 JSON、未知枚举、超限列表或 HTML | 有界安全错误；不插入缓存、不以类型断言渲染 |
| 失败 | 旧搜索慢于新搜索；切换账号时旧查询/写操作迟到 | 不覆盖新状态；旧身份回调不触发通知或导航 |
| 失败 | 任意受保护请求 401；另一个请求仅返回 403 | 401 统一清理旧身份；403 保留会话但不保留无权内容 |
| 失败 | A 注销后 B 登录、同一用户重新登录、多标签页撤销 | Query/Router/组件与未来播放器没有旧身份残留；浏览器 HTTP 缓存单独验证 |
| 失败 | 管理操作 409、取消等待但后端已提交、断网结果未知 | 保留草稿或结果未知状态，提供核对路径；不显示假成功或假撤销 |
| 成功/失败 | 离线加载已构建界面、字体请求失败、混合字集长标题 | 无必需外部字体请求；系统 fallback 可读，键盘焦点、换行和窄屏不破坏主流程 |
| 成功 | 干净安装、类型检查、定向交互测试、生产构建与 Go 同源访问 | manifest/lock 一致，实际 UI 可加载；开发不引入第二套认证 |

以上是 M1/后续任务的验收输入。M1a 已用 Vitest（含迟到响应与会话代次、结果不确定与同键重试、409）、Playwright（初始化与登录、
401 与 403、无第三方请求、五种宽度与 axe）以及一次真实接口冒烟执行了其中的会话、写操作、损坏响应、同源与字体相关案例；
搜索、分页、详情与前进后退的案例由 M1b（2026-10-03）执行：Vitest 页面级测试与 Playwright 覆盖搜索与翻页的地址、后退、前进、刷新与越界页码，
详情的深链刷新与版本选择（`release` 参数）、只读证据的权限（普通用户从不请求管理员接口）、损坏或迟到响应沿用 M1a 的请求层测试；字体请求全部被拦截时，
混合字集的长标题（超过 1000 字）在五种宽度下可读、没有整页横向溢出；隔离 PostgreSQL 上的真实接口冒烟 17 项通过。M0 当时只做文档范围、引用和空白检查。

## 8. 待批准决策

1. ~~是否在 M1 同时采用 TanStack Router/Query~~ —— **已批准（2026-09-28）**：两者同时采用，具体适配器随 M1a 的兼容集合核验。
2. ~~是否采用 CSS Modules + 少量全局变量~~ —— **已批准（2026-09-28）**：采用；进入生产设计合同的视觉变量在 M1a 按 `frontend/DESIGN.md` 核对。
3. ~~首发是否自托管三套候选字体~~ —— **已批准（2026-09-28）**：用 Fontsource 自托管 400 与 600 字重，保留 `unicode-range` 分片与系统回退；体积与布局阈值、许可复核在 M1a 完成。
4. ~~查看者会话代次、跨标签页失效、Router/Query 的统一清理及 HTTP 缓存策略是否作为 M1a 的安全验收门禁？~~ —— **已批准（2026-10-01）并实施**：
   会话代次、统一清理顺序（取消 → 清空 → 重新执行路由守卫）、查询键带代次、已登录布局按代次重挂、写操作回调检查代次、
   `BroadcastChannel` 广播与重新可见时核对，均有 Vitest 与 Playwright 覆盖；API 请求 `no-store`。
5. ~~精确依赖组合、支持浏览器、聚焦重新验证/重试策略~~ —— **已批准（2026-10-01）并实施**：依赖精确固定，TypeScript 6 与 Router 的类型在
   `strict` 加 `noUncheckedIndexedAccess`、`exactOptionalPropertyTypes`、`noFallthroughCasesInSwitch` 下通过检查；浏览器矩阵体现在 Vite
   `build.target`；真实构建与资产漂移检查已恢复，CI 增加 Chromium 冒烟。
6. `/api/` 未匹配路由、既有封面缓存头等后端行为调整须独立列项，不因本 ADR 存在自动获批；`/api/` 未匹配路由已立项为 F1（2026-09-29 完成），
   封面与静态资源的缓存头已立项为 F3（2026-10-02 批准并完成，结论见决策记录）。此后其他后端行为调整仍须独立列项。

## 9. 外部资料与核验范围

以下均在 **2026-09-27** 查阅。编号用于对应正文论点；URL 以代码保存。资料证明技术机制，
不证明本项目已经采用、通过浏览器测试或完成许可证义务。精确版本兼容性留待实施时验证。

| 编号 | 官方/原始资料 | 本文使用的证据 |
| --- | --- | --- |
| F01 | React：`https://react.dev/learn/build-a-react-app-from-scratch` | 从构建工具起步仍需自行选择路由和数据获取方案 |
| F02 | TanStack Router：`https://tanstack.com/router/latest/docs/framework/react/guide/external-data-loading` | Router 可与外部数据获取/缓存库协作，避免双重所有权 |
| F03 | TanStack Query：`https://tanstack.com/query/latest/docs/framework/react/guides/query-keys` | query key 应唯一描述数据并覆盖查询依赖变量 |
| F04 | TanStack Query：`https://tanstack.com/query/latest/docs/framework/react/guides/query-cancellation` | 提供 AbortSignal；默认卸载行为与消费 signal 后的取消语义不同 |
| F05 | TanStack Query：`https://raw.githubusercontent.com/TanStack/query/main/packages/query-core/src/queryClient.ts` | QueryClient 的取消、移除与清理能力；不能据此推导 Router、播放器或 HTTP 缓存已清理 |
| F06 | Vite：`https://vite.dev/guide/features` | CSS Modules 支持；TypeScript 转换与类型检查职责不同 |
| F07 | Fontsource：`https://fontsource.org/docs/getting-started/install` | 自托管安装方式及默认引入的字重/字集范围 |
| F08 | Fontsource：`https://fontsource.org/docs/getting-started/subsets` | 字集与 `unicode-range` 对加载范围的影响 |
| F09 | Fontsource：`https://fontsource.org/docs/getting-started/display` | `font-display` 的可选策略与加载/替换取舍 |
| F10 | Google Fonts 原始字体许可：`https://raw.githubusercontent.com/google/fonts/main/ofl/cormorantgaramond/OFL.txt`；`https://raw.githubusercontent.com/google/fonts/main/ofl/lora/OFL.txt`；`https://raw.githubusercontent.com/google/fonts/main/ofl/notoserifsc/OFL.txt` | 三套上游字体分别附 OFL；实际发行包与条件须另行核对，不能从聚合平台推定全部合规 |
| F11 | npm：`https://docs.npmjs.com/cli/v11/commands/npm-ci` | manifest/lock 不一致会失败；`npm ci` 不改写锁文件。v11 文档不表示已替项目选定 npm 版本 |
| F12 | RFC 9111：`https://www.rfc-editor.org/rfc/rfc9111.html#section-5.2.2` | `private` 不禁止私有缓存；`no-store` 不等于追溯清除旧响应 |
