# Core 0 后续技术债

本文件从 [Core 0 当前运行合同](./core0-runtime-contracts.md)移出（原样搬移，只把条目里的“见上文”改成链接），
因为总览接近 24 KB 的上限。被划掉的条目是已经修复的历史，保留是为了让后来的人知道它们为什么存在。

- HTTP 请求现在通过 `http.request.completed` 事件记录状态码、耗时和路由模板；M3 起发行修正、归组与撤销写操作记录，用户
  创建/禁用/会话撤销仍无幂等键与操作记录（M3 裁决 G6 不纳入，2026-11-03 起不再做）。
- ~~前端源码已删除~~：2026-10-01 由 M1a 按 Classical 设计重建，依赖精确固定（见 ADR 0001 决策记录）。
- M1a 前端发现的后端缺口（M1a 不改后端，如实呈现，记入后续任务）。2026-10-02 由 F3 修复的条目划掉，其余仍待办：
  - ~~没有扫描历史列表~~：F3 新增 `GET /api/v1/scans`（管理员）与扫描目录页的“扫描记录”，见 [扫描协调合同](./scan-coordination-contracts.md)。
  - ~~`/auth/me` 不返回用户 ID~~：F3 起 `/auth/me`、登录与初始化管理员的响应都带 `id`，前端按 ID 判断“你”与同一查看者。
  - 成员列表无分页，不能创建管理员或修改角色（M3 没有引入 `editor`，仍是候选）。
  - 成员写操作无幂等键：F3 裁决延到 M3，**M3 裁决（G6）不纳入**，2026-11-03 撤销接入任务（R4），现状见 [身份与成员合同](./identity-contracts.md)；前端保持“结果不明时刷新列表核对”。
  - ~~`listUsers` 会静默跳过读取失败的行~~、~~`createUser` 与 `setupAdmin` 把任何写入失败映射成 409~~、~~`invalid_input` 的文案写
    “12 个字符”而规则按字节~~（另有过长密码在 bcrypt 处变成 500）、~~`PATCH /users/{id}` 与撤销会话对格式不对的 ID 返回 503，撤销
    未知用户也返回 200~~：F3 已修复，见 [身份与成员合同](./identity-contracts.md)。
  - ~~401 信封的 message 与角色无关~~：F3 起统一为“需要登录”。
  - 没有查询允许根目录的接口，新增目录只能手输服务器上的绝对路径。
  - ~~`httpserver.Frontend` 不设缓存头；`/assets/`、`/licenses/` 这类目录路径会返回目录列表；封面缓存头 `private, max-age=86400`~~：
    F3 已修复，见 [Core 0 运行合同](./core0-runtime-contracts.md) 的“HTTP 缓存”。
- ~~M1b 真实接口冒烟（2026-10-03，见任务 `09-28-classical-m1b-album-browsing` 的 `research/real-api-smoke.md`）发现的封面发现缺口~~：
  2026-10-03 由 F4（任务 `10-03-classical-f4-artwork-discovery`）修复，规则见 [归组与发行](./library-scan-grouping.md)的“封面来源与文件夹封面发现”：
  - ~~CUE 专辑拿不到文件夹封面~~：虚拟路径推不出真实目录；现在用观察的 `Directory`（sheet 所在目录）。
  - ~~多碟专辑只在第一张碟的文件夹里找封面~~：现在先查发行目录（各碟的父目录），再按碟序查各碟目录。
  - ~~封面文件名只认全小写~~（F4 规划时的只读统计发现）：Linux 区分大小写，`Cover.JPG` 等找不到；现在不区分大小写。
  - 查找范围仍限于发行自己的目录：Box 叶子不查 Box 父目录，CUE 不查跨目录的音频目录。
- ~~未匹配的 `/api/` 仍落入前端 fallback~~：2026-09-29 由 F1 修复。`/api` 兜底返回 JSON 404/405/503，
  完成日志的 `route_template` 记为 `<unmatched>`，见 [Core 0 运行合同](./core0-runtime-contracts.md) 的“校验与错误矩阵”。
- ~~2026-09-27 静态审查：停用/恢复指纹未含路由 root ID，同键同 body 跨目标会重放前一个 root 的结果~~：
  2026-09-29 由 F2 动态复现并修复。指纹现含小写 root ID，同键请求串行，旧记录不做兼容，见
  [目录与扫描流程合同](./library-scan-contracts.md)的“目录新增与操作幂等”。目录操作仍是窄实现，不是通用幂等模板：失败尝试不写记录，通用操作合同由 M3 建立（`operations.Run`，目录操作没有迁入），
  见 [ADR 0003](../../../docs/architecture/adr/0003-governed-operations.md)。
