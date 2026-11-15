# 设计

## 组件边界（均在 `frontend/src/features/player/components/`）

- `player-controls.tsx`
  - 新增 `PlayModeButton`：替代 `ShuffleButton` / `RepeatButton`。顺序表 `sequential → repeat_all → repeat_one → shuffle → sequential`；
    图标 顺序 `ArrowRight`（lucide 无“顺序”专用图标时用 `ListOrdered` 或 `Repeat` 加未开启态，实现时二选一并在规范记录）、列表循环 `Repeat`、单曲循环 `Repeat1`、随机 `Shuffle`；
    名称“播放模式：顺序 / 列表循环 / 单曲循环 / 随机”，悬停提示同名；非顺序时 `aria-pressed` 与现有 `.mode[data-on]` 圆点。`repeatLabels` 改为四种模式的标签表。
  - `TransportControls` 去掉 `modes`，改为 `leading` / `trailing` 两个插槽（ReactNode），由调用方放信号与模式；compact 窄屏时插槽随上一首一起隐藏（沿用 `.desktop`）。
  - 新增 `VolumeButton`：喇叭（静音时 `VolumeX`）图标按钮，点开锚定弹层（沿用 `DeviceDialog` / 信号路径的 `ModalDialog` + 锚点坐标做法，按钮在视口上半部时向下弹），
    内含竖向音量滑块（`aria-label="播放器音量"`、`aria-valuetext` 百分比不变）与静音开关；不可调时按钮禁用、悬停提示“这台设备不能调音量”。删除 `VolumeControl`。
- `signal-path.tsx`：`SignalPathButton` 不变；新增固定宽度的外层（与图标按钮同宽），没有 `signal` 时渲染空位。
- `player-bar.tsx`：左段去掉圆点；中段 `TransportControls leading={信号位}`；右段 `PlayModeButton`、队列、分隔线、设备、`VolumeButton`。窄屏 `.desktop` 规则保持，模式与喇叭只在宽屏。
- `fullscreen-player.tsx`：宽屏 topbar 只留收起；控制台一行 信号 设备 ⏮ ▶ ⏭ 模式 喇叭；去掉音量行与标题旁圆点。窄屏传输行 `leading=信号位`、`trailing=模式`，底部行不变，标题旁去掉圆点。
- `device-picker.tsx`：弹层方向判断已有（上半部向下），全屏里按钮移到下方后自动向上弹，不改。

## 概览

- `features/catalog/components/library-stats.tsx` 改为卡片：标题“资料库”，四项纵向排列（数值右对齐、等宽数字 `tabular-nums`，单位在后），链接与空态规则不变；
  外观沿用右栏 `StatusTable` 的面板样式，使两块在右栏成组。
- `overview-page.tsx` / `.module.css`：右栏 `side` 内依次放 `LibraryStats`、`StatusTable`；删除 `ledger` 区。窄屏单列时 side 在 main 后，自然得到 R4 顺序。

## 封面与滚动条

- `player-bar.tsx` 的 `ExpandArtworkButton` 在封面上叠一层 `aria-hidden` 遮罩（`ChevronUp` 20px，底色 `rgb(0 0 0 / 40%)`），`:hover` / `:focus-visible` 时 `opacity: 1`，120ms 过渡，`prefers-reduced-motion` 下去掉过渡；`.cover` 改 `cursor: pointer`。
- 在 `styles/` 全局样式加两个工具类（或 CSS 自定义属性组）：`thin-scroll`（`scrollbar-width: thin; scrollbar-color: color-mix(in srgb, currentColor 28%, transparent) transparent;` 加 `::-webkit-scrollbar` 6px 透明轨道、圆角滑块的兜底）与 `no-scrollbar`（沿用 `shelf.module.css:20` 的隐藏写法）。
  组件 CSS 里以同样声明写在各滚动容器上也可，实现时选一种并在规范记录；不改页面主滚动条。
- 全屏根元素与 `--signal-card-bg` 深色时的弹层加 `color-scheme: dark`。

## 兼容与风险

- 播放模式语义、服务端命令不变，只是入口合并；e2e 与组件测试里按“随机播放”“循环：…”名称找按钮的用例要改为“播放模式：…”。
- 音量滑块进入弹层后，测试需先点喇叭再操作滑块。
- 回滚：单分支，回退合并提交即可。
