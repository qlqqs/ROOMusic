# 设计

## 视觉方案（frontend-design 两步法）

沿用现有令牌，不另起配色：纸面 `--color-bg #f3f2f2`、面板 `--color-surface #eae9e9`、黄铜 `--color-accent #b68235`、小字 `--text-accent`（accent-700）、正文 `--color-text`。字体沿用 `--font-body`，字号 14 / 13 / 12 三级。

- 版面：一列，左对齐。分区标题是普通 13px 中文小标题（不用全大写、不用序号——顺序由列表本身表达，拖拽把手已足够）。
  ```
  队列                         [存为歌单] [清空] [✕]
  23 首 · 1 小时 32 分
  › 已播放 5 首
  正在播放
  ┌──────┐ 歌名                         4:10
  │封面 ▮▯▮│ 艺人
  └──────┘
  接下来
  ⠿ ▢ 歌名一                     3:21  ✕ ⋯
  ⠿ ▢ 歌名二                     4:02
  ```
- 行是平的：无卡片、无阴影、无分割线，行距 4px，悬停只加 `--color-hover` 底色、8px 圆角；“正在播放”一行用 `--color-selected` 底色和 2px 黄铜左边线（常驻，状态变化不挪动文字）。
- 记忆点只放一处：“正在播放”封面角上的黄铜律动条（3 根 2px 宽，高度用 `transform: scaleY` 循环，错开相位）。其余全部安静。

自查：初稿用了三张圆角卡片分隔三段、段标题全大写加序号，属于“SaaS 卡片套件”与模板化标签；改为平铺列表与普通小标题，唯一强调留给律动条。

## 组件（`frontend/src/features/player/components/`）

- `queue-drawer.tsx`：抽屉样式与动画；窄屏底部面板（同一个 `ModalDialog`，媒体查询切换贴边方向与动画）。关闭动画：在 `onRequestClose` 时先加 `data-closing`，`animationend` 后再真正关闭；减少动态效果时直接关闭。
- `player-queue.tsx` 拆分：`QueueHeader`、`QueueSection`（已播放用现有 `Disclosure`）、`QueueRow`（`useSortable`，把手 `setActivatorNodeRef`）、`NowPlayingRow`、`PlayingBars`。
  - 一个 `DndContext` + 一个 `SortableContext` 覆盖全部条目（含正在播放行，但正在播放行不显示把手、不可拖），放下时 `to_index` 即目标在完整队列中的下标。
  - 乐观更新：放下即本地 `arrayMove` 显示，命令失败时以服务端推送为准回滚（与 `engine` 现有命令流一致，具体在 `controller.moveEntry` 内处理）。
  - ⋯ 菜单用 `MenuButton`。
- `zones/controller.ts` `moveEntry(entryId, toIndex)`；`zones/decoders.ts`、`zone-store.ts` 解析并应用 `move {entry_id, to_index}`。
- 滚动条用前一任务的细滚动条工具样式。

## 服务端（`backend/internal/zones/`）

- `httpapi/input.go`：`move_entry` 必填 `entry_id`、`to_index`（非负整数）。
- `commands.go`：越界 → 400 `invalid_command`（沿用现有错误形状）；相同位置 → noop；否则 `queue.MoveEntryTo`，`QueueChange{Kind:"move", EntryID, ToIndex}`。
- `queue/queue.go`：`MoveEntryTo(state, entryID, toIndex)` 取出再插入；当前项、历史、袋子按 entry_id 记录，不受影响。
- `store_entries.go`：`MoveEntry` 在事务内按区间平移 `position`（`UNIQUE(zone_id,position)` 是 DEFERRABLE INITIALLY DEFERRED，事务内直接平移即可，见 `zones-data-contracts.md:13`）；删除 `SwapEntries`。
- 契约文档 `.trellis/spec/backend/playback-zone-contracts.md` 同步。

## 风险与回滚

- 协议是破坏性变更，但前后端同仓同版本发布，无其他客户端；前后端在同一分支一起合入。回滚即回退合并提交。
- 位置平移的并发：命令已在播放区串行处理（现有 `commands.go` 流程），不额外加锁。
