# F4 端到端冒烟（合成素材）

> 2026-10-03。只用合成素材，没有读写 `music/`，也没有碰用户自己的 `roomusic` compose 项目。
> 素材由 M1b 冒烟用的临时生成器产生（`/tmp`，不入库）：74 条合成 FLAC，62 个组；封面都是合成 PNG。

## 环境

- `roomusic-test` compose 项目的 PostgreSQL 18（`127.0.0.1:55432`），后端是本次提交构建的二进制，监听 `127.0.0.1:18082`。
- 驱动脚本通过 REST 完成：初始化管理员、登录、添加目录、扫描到终态、分页读取目录列表。
- 结束后后端进程按 PID 结束，`docker compose -p roomusic-test down -v` 清理了容器、数据卷与网络。

## 结果

| 阶段 | 素材改动 | 扫描 | 有封面的组 | 关键断言 |
| --- | --- | --- | --- | --- |
| A | 与 M1b 冒烟一致（双碟专辑的封面在 `CD1/`），另给没有封面的 `prelude-001` 放一个 `Folder.PNG` | 成功，诊断 0 | 15 | CUE 专辑有封面（800×500）；双碟专辑有封面（600×600，在 CD1）；`Folder.PNG` 大小写变体被找到；没有封面的专辑仍没有（Goldberg、`prelude-002`） |
| B | 双碟专辑的封面挪到父目录，改名 `Cover.PNG` | 成功，诊断 0 | 15 | 双碟专辑仍有封面（600×600），来自父目录；其余与 A 完全相同 |
| C | 父目录 `Cover.PNG` 与 `CD1/cover.png`（另一张 300×300）同时存在 | 成功，诊断 0 | 15 | 仍是父目录那张（600×600）：父目录优先于第一张碟 |

- 15 = CUE 专辑 1 + 双碟专辑 1 + 长标题专辑 1 + 每 5 张单曲专辑里带封面的 11 张 + `Folder.PNG` 的 1 张，与预期一致。
- A、B、C 三次的“有封面的组”集合相同；`release_artworks` 的来源全部是 `folder`（15 行）。
- M1b 冒烟时 CUE 专辑没有封面；F4 之后有了。其余专辑的封面与 M1b 冒烟一致。
- CUE 专辑的封面经 `/api/v1/artworks/{id}` 返回 200，`image/png`，`Cache-Control: private, no-cache`，带 `ETag`，内容是有效 PNG。

## 没有覆盖的

- 浏览器页面截图没有重跑：F4 不改 REST 与前端，API 响应样本测试没有变化，前端显示封面的逻辑与 M1b 一致。
- 真实库：由用户重扫后确认，预期变化见 [real-library-artwork-layout.md](real-library-artwork-layout.md)。
