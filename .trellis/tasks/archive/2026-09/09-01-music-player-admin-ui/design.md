# 技术设计

## 架构边界
继续由 `App` 负责会话与 API 请求协调；将视图拆为同文件内的语义组件，避免引入新状态库。服务端状态保留在现有 React state，播放状态为本地 UI state。API 解码器仍集中在 `api.ts`，组件不直接调用 fetch。

## 信息架构
- 应用壳：侧栏（Library / Queue / Admin）、顶部搜索与账户菜单。
- Library：统计摘要、发行版本网格、详情面板。
- Queue：当前队列与可点击曲目。
- Admin：用户、目录、扫描、操作历史四个分区，仅管理员渲染。
- Player bar：固定底栏，展示封面缩略图、曲目、控制、进度与音量占位。

## 视觉与交互
深色中性背景配青绿色强调色和珊瑚色状态色，使用高对比边框分层；8px 以下圆角。布局优先 CSS grid/flex，移动端使用媒体查询堆叠。播放控制使用 lucide 不可用时使用文本符号与 aria-label，避免伪造复杂图标。

## 数据流
保留当前 setup/session/release/admin effects 与请求函数。点击发行版本详情继续调用 `/api/v1/releases/:id`；点击曲目只写入本地 `nowPlaying` 与 `isPlaying`，不新增网络请求。搜索继续同步 URL `q`。

## 回滚
改动限定 `frontend/src/main.tsx`、`frontend/src/styles.css`，可通过恢复这两个文件回滚；后端与 API 解码器不改动。
