# 音乐目录浏览器：技术设计与接口合同

需求见 [prd.md](./prd.md)，产品决定见父任务 D9。本子任务不需要数据库迁移。

## 1. 边界与归属

| 层 | 改动 | 位置 |
| --- | --- | --- |
| 文件系统列举（纯函数，无 DB） | 新增 `ListFolders` | `backend/internal/library/folders.go`（与 `roots.go` 的 `ValidatePath` 同包） |
| REST 传输 | 新增 `GET /api/v1/library-folders` handler 与路由 | `backend/internal/library/httpapi/folders.go`，`handlers.go` 的 `Register` 加一行 |
| 装配 | 无改动：`Handlers` 已持有 `allowedRoots` 与 `database` | `backend/internal/app/app.go:102` 不变 |
| 前端 api / hook | decoder、endpoint、query key、`useFolderListing` | `frontend/src/features/library/{api,hooks,query-keys.ts}` |
| 前端组件 | 新增 `FolderBrowser`，改 `AddRootForm` | `frontend/src/features/library/components/` |
| 测试替身 | 单元替身与 e2e 模拟各加一个路由 | `frontend/src/test/fake-library.ts`、`frontend/e2e/support/api-mock.ts` |

`library` 包只负责“这个路径能不能浏览、里面有哪些子文件夹”；“是否已添加为扫描目录”需要查表，放在 handler（沿用 `library/httpapi` 直接执行 SQL 的既有做法，只多一条只读查询）。

## 2. 接口合同

`GET /api/v1/library-folders?path=<绝对路径>`，仅管理员；只读，不需要同源校验和幂等键。

- `path` 缺省或为空：起点。只有一个允许目录时直接返回它的内容；有多个时返回允许目录列表。
- `path` 非空：返回该文件夹的子文件夹。

成功 `200`：

```json
{
  "path": "/music",
  "parent": null,
  "can_go_up": false,
  "registered": false,
  "truncated": false,
  "items": [
    { "name": "Classical", "path": "/music/Classical", "registered": true },
    { "name": "Jazz", "path": "/music/Jazz", "registered": false }
  ]
}
```

| 字段 | 含义 |
| --- | --- |
| `path` | 当前文件夹的规范化绝对路径；`null` 表示“允许目录列表”这一层（仅多个允许目录时出现） |
| `parent` | 上一级文件夹路径；在允许目录本身或列表层为 `null` |
| `can_go_up` | 能否返回上一级。`parent` 为 `null` 而 `can_go_up` 为 `true`，表示回到允许目录列表（不带 `path` 再请求一次） |
| `registered` | 当前文件夹本身是否已登记为扫描目录（含停用）；列表层恒为 `false` |
| `truncated` | 子文件夹超过上限、只返回了前一部分 |
| `items[].name` / `path` / `registered` | 显示名、可直接回传给本接口或填进路径框的绝对路径、是否已登记。列表层的 `name` 为完整路径 |

列表层示例：`{"path":null,"parent":null,"can_go_up":false,"registered":false,"truncated":false,"items":[{"name":"/music","path":"/music","registered":false},{"name":"/srv/audio","path":"/srv/audio","registered":false}]}`。

**条目规则**

- 只列 `os.ReadDir` 结果里 `Type().IsDir()` 为真的条目（目录符号链接的类型是 `ModeSymlink`，自然排除）；排除以 `.` 开头的名字；排除不是合法 UTF-8 的名字（JSON 往返会变成替换字符，回传后对不上）。
- 上限 **2000** 条；超过时按字节序取前 2000 条并置 `truncated: true`。
- 后端按字节序返回（`os.ReadDir` 已排好），前端用 `Intl.Collator("zh-CN", { numeric: true, sensitivity: "base" })` 重排，让 `Disc 2` 排在 `Disc 10` 前、大小写不分。
- 允许目录列表层：对每个配置的允许目录取 `filepath.EvalSymlinks` 后的真实路径（这样回传时能通过 `ValidatePath`），解析失败或不是目录的跳过；去重后按配置顺序。只剩一个时按“单个允许目录”处理。

**错误**（统一错误信封，`message` 不含任何路径）

| 情况 | HTTP / code | message |
| --- | --- | --- |
| 未登录 | `401`（沿用 `RequireAdmin`） | 沿用 |
| 非管理员 | `403 forbidden`（沿用 `RequireAdmin`） | 需要管理员权限 |
| 非绝对路径、非规范形式（含 `..`、`.`、`//`、结尾 `/`）、不存在、不是目录、任一层是符号链接、越出允许目录、无权读取 | `400 invalid_library_path` | 打不开这个文件夹 |

与新增目录共用 `invalid_library_path`，前端只按 code 分支；越界与不存在不作区分，避免借此探测允许目录之外的文件系统。

**接口样本**：在 `api_samples_integration_test.go` 创建 libraryA、libraryB 之后录制：

- `library-folders/start`（不带 `path`，样本应用只有一个允许目录，列出 libraryA 等，其中 `registered: true`）；
- `library-folders/child`（进入一个子文件夹，含 `parent` 与 `can_go_up: true`）；
- `errors/invalid-library-folder`（`path=/etc`，400）。
- 普通成员调用只断言 `403`，不录样本（`forbidden` 已有样本）。

样本禁止出现允许目录的临时路径，而这个接口本来就返回绝对路径：录制前把响应体里的 `application.allowedRoot`（及其 `EvalSymlinks` 形式）替换为 `/music`，再交给 `samples.record`，禁止清单不放宽。前端 `api-contract.test.ts` 的样本表加三项；多个允许目录的列表层没有后端样本，由 `decoders.test.ts` 的手写正例覆盖。

## 3. 安全边界

- `path` 非空时先要求 `path == filepath.Clean(path)` 且是绝对路径，再直接调用 `library.ValidatePath(path, allowedRoots)`，判定与新增目录完全一致（不跟随符号链接、必须在允许目录之内、必须是真实目录）。浏览器里能进入的文件夹，就是添加时能通过校验的文件夹。
- 列出子文件夹时用 `os.ReadDir` 的条目类型（不 `Stat` 跟随），符号链接不出现；返回的子路径是 `filepath.Join(当前真实路径, name)`，不会越界。
- 允许目录本身对管理员可见（PRD R6）；普通成员和未登录者在鉴权阶段就被拒绝，拿不到任何路径。错误响应仍不含路径。
- 读取在请求内同步完成，有 2000 条上限；不递归、不统计子目录内容，单次请求只做一次 `ReadDir`。

## 4. 前端组件结构与状态

**数据层**

- `decodeFolderListing`（严格解码，`path`/`parent` 允许 `null`）；`fetchFolders(path: string | null, signal)`，`path` 为 `null` 时不带查询参数。
- `libraryKeys.folders(generation, path)`；`useFolderListing(path, enabled)` 只在浏览器打开时请求，`select` 里按 Collator 重排。
- 新增目录成功后，在 `use-root-commands` 里让 `library/folders` 前缀失效，“已添加”标记随之更新。

**`FolderBrowser`**（`folder-browser.tsx` + `.module.css`）

- Props：`{ id: string; initialPath: string | null; onSelect(path: string): void; onClose(): void }`。内部状态只有 `current: string | null`（当前请求的路径）。
- 布局：紧贴路径框下方的面板，最大高度约 320px，列表区域滚动；375 宽度下占满表单宽度，长路径 `overflow-wrap: anywhere`。
- 顶部一行：返回上一级图标按钮（`aria-label="上一级"`，`can_go_up` 为假时不显示）、当前路径文本（`tabIndex=-1`，进入新文件夹后焦点移到这里，读屏会读出新位置）、“选择”（列表层不显示）。
- 列表 `<ul>`：每行左侧是文件夹名按钮（点它进入），已登记的显示淡底色标签“已添加”；右侧“选择”文字按钮，无边框，`aria-label="选择 <名字>"`。
- 文案只有三句：空文件夹“这里没有子文件夹。”；截断“文件夹太多，只显示了前 2000 个。”；出错“打不开这个文件夹。”加一个“回到开头”按钮（`current` 置 `null`）。加载中沿用 `Skeleton`。
- 不使用 `role="listbox"`/`aria-activedescendant` 等复合控件语义：行内就是普通按钮，Tab 顺序自然，读屏按“按钮 + 列表”理解，不需要额外键盘处理。

**`AddRootForm` 改动**

- 新增 `browsing: boolean`。路径框 `onClick` 打开；路径框里按 `ArrowDown` 也打开（键盘入口）。仅 Tab 聚焦不自动打开，避免键盘用户经过时弹出。
- 路径框带 `aria-expanded={browsing}`、`aria-controls={browserId}`；浏览器在 DOM 上紧跟路径框，Tab 直接进入。
- 外层包一个 `div`：`pointerdown` 落在其外收起；在其内按 `Esc` 收起并把焦点还给路径框（沿用 `menu-button.tsx` 的做法）。
- 选中：`onPathChange(path)`、收起、焦点回路径框，回车或点“添加目录”提交。用户在路径框里输入时收起浏览器，手动输入与校验保持现状。
- 路径框的说明句改为“服务器上的文件夹，例如 /music/classical”（由主会话最终定稿，保持一句）。

样式遵循 ui-minimal 之后的规范：无衬线字体、紧凑字阶，“选择”与“上一级”为无边框文字/图标按钮，只有“添加目录”保留主要按钮样式。

## 5. 测试计划（只覆盖本次改动）

后端：

- `backend/internal/library/folders_test.go`（`t.TempDir`，无 DB）：① 只列子文件夹，文件、隐藏文件夹、目录符号链接都不出现，顺序为字节序；② 超过上限时截断并标记；③ `/etc`、`<root>/../x`、结尾带 `/`、不存在、普通文件、符号链接目录都返回错误；④ 起点：单个允许目录直接进入，多个时返回列表层，允许目录本身是符号链接时以真实路径列出。上限通过参数传入，测试用小上限，不造 2000 个目录。
- 集成：在 `TestPostgreSQLAPIResponseSamples` 录制第 2 节三个样本，并加一次普通成员调用断言 `403`。

```bash
cd backend && go test ./internal/library/ -run TestListFolders -count=1
ROOMUSIC_UPDATE_API_SAMPLES=1 ROOMUSIC_TEST_RUN=TestPostgreSQLAPIResponseSamples ./scripts/test-integration.sh   # 首次生成样本
ROOMUSIC_TEST_RUN=TestPostgreSQLAPIResponseSamples ./scripts/test-integration.sh                                  # 确认样本稳定
```

前端：

- `api-contract.test.ts` 样本表加三项；`decoders.test.ts` 加列表层正例与一个反例（`items[].path` 缺失）。
- `src/app/library-roots.test.tsx` 加两例：① 点路径框展开 → 进入子文件夹 → 当前文件夹顶部有“上一级”，返回到允许目录后不再显示 → 点某行“选择”填入路径并收起 → 添加成功（A1）；② `Esc` 收起且焦点回到路径框，点外面也收起。`fake-library.ts` 增加 `GET /api/v1/library-folders`，数据取自样本。
- `e2e/library.spec.ts` 加一例：浏览并选择后添加，1440 与 375 宽度各截一张图（A4）；`api-mock.ts` 加同一路由。

```bash
cd frontend && npx vitest run src/app/library-roots.test.tsx src/test/api-contract.test.ts src/features/library/api/decoders.test.ts
cd frontend && npm run typecheck && npx eslint src/features/library src/app/library-roots.test.tsx
cd frontend && npx playwright test e2e/library.spec.ts -g "浏览"
```

不跑全量 Go / vitest / Playwright：改动局限在 library 一个接口和添加目录表单。

## 6. 风险与回滚

- **大目录**：一次 `ReadDir` 读整个目录，上万条时有几十毫秒级开销，可接受；返回体被 2000 条上限限制在约 200 KB 内。
- **网络盘或慢盘**：请求跟随 `request.Context()`，前端切换文件夹时取消上一次请求；不另加超时。
- **样本路径替换**：若遗漏某种路径形式，禁止清单会让样本测试失败，不会把真实路径写进样本。
- **回滚**：纯新增接口与前端组件，无迁移；回退对应提交即可，手动输入路径的旧流程一直可用。

## 7. 待主会话裁决

1. **接口路径**：推荐 `GET /api/v1/library-folders`。备选 `GET /api/v1/library-roots/browse`——与现有目录资源挂在一起，但 `library-roots/{id}` 下的子路径语义会混淆。
2. **是否标记“已添加”**：推荐标记（淡底色标签，仍可选择）。管理员一眼看出哪些已加过，避免重复添加；代价是 handler 多一条 `SELECT path FROM library_roots WHERE path = ANY($1)`，以及新增成功后让缓存失效。不标记则后端是纯文件系统函数，但重复添加只能靠提交后的“已经添加过”提示发现。
3. **`..` 与非规范路径**：推荐严格拒绝（要求 `path == Clean(path)`），与 A2 字面一致；前端只回传服务端给出的路径，不受影响。备选是先 `Clean` 再校验（`/music/a/../b` 视为 `/music/b`），更宽松但 A2 的“带 `..` 被拒绝”就只对越界情形成立。
4. **条目上限**：推荐 2000，超过时显示一句截断提示，用户仍可手动输入。备选分页（`page`/`page_size`）更完整，但一个文件夹几千个子文件夹的情况很少，分页会让组件多一套翻页状态。
5. **打开时的起点**：推荐始终从起点打开（实现最简单，符合 R2）。备选是路径框里已有合法路径时从该文件夹打开，再次调整更顺手，但要处理“路径无效时退回起点”的分支；可在用户反馈后再加。
6. **排序位置**：推荐前端用 `Intl.Collator`（数字感知、中文按区域规则）重排，后端只按字节序截断。代价是截断时“前 2000 个”按字节序而非显示顺序选取；备选后端做自然排序，需要在 Go 里自己实现中文与数字规则，不值得。

## 8. 裁决结果（主会话，2026-11-07）

| 点 | 裁决 | 理由 |
| --- | --- | --- |
| 1 接口路径 | 采纳 `GET /api/v1/library-folders` | 与目录资源 `library-roots/{id}` 不混淆 |
| 2 标记“已添加” | 采纳：淡底色标签，仍可选择 | 管理员一眼看出哪些加过，只多一条只读查询 |
| 3 非规范路径 | 采纳严格拒绝（`path == Clean(path)`） | 前端只回传服务端给的路径，不受影响 |
| 4 条目上限 | 采纳 2000 条加截断提示，不分页 | 截断提示改成“文件夹太多，没有全部列出，可以直接输入路径。”，不写数字，告诉用户怎么办 |
| 5 打开时的起点 | 采纳始终从起点打开 | 添加目录是很少用的操作，多点一两下可以接受；用户反馈后再加 |
| 6 排序位置 | 采纳前端 `Intl.Collator` 重排 | 后端不必实现中文与数字的自然排序 |
| 路径框说明 | 删掉说明句，只留标签“目录路径” | 有了浏览器后这句不再必要（`~/.claude/CLAUDE.md` 的文案约定）；手动输入出错时由现有的错误提示说明 |
