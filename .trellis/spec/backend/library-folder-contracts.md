# 音乐目录浏览合同

## 1. 范围与触发

新增或修改目录浏览的范围、列表、鉴权或响应时读取本合同。文件系统列举归 `library.ListFolders` / `library.ListFoldersWithin`，REST、鉴权与登记标记归 `library/httpapi`；不修改源目录，不递归，不改变新增扫描目录的 `ValidatePath`，不需要迁移。

## 2. 接口签名

`GET /api/v1/library-folders?path=<规范绝对路径>`，仅管理员，只读，不要求 Origin 或幂等键。

```json
{"path":"/","parent":null,"can_go_up":false,"registered":false,"selectable":false,"truncated":false,"items":[{"name":"music","path":"/music","registered":false,"selectable":true}]}
```

`path` 必为非空规范绝对路径；普通模式仅 `/` 的 `parent` 为 null，其余字段必传；`items` 无结果为 `[]`。`selectable` 表示当前目录或条目是否位于允许根内，允许根及其内部为 true，上层路径为 false。`registered` 表示已登记为扫描目录，包含停用状态，不阻止浏览或选择。

目录内模式：`GET /api/v1/library-folders?root_id=<uuid>[&path=<规范绝对路径>]`，同样仅管理员。`library.ListFoldersWithin(input, rootPath, allowedRoots, limit)` 复用普通模式的根内列举；先鉴权，再校验 UUID、通过 `LoadRootLocation` 查登记根，然后才访问文件系统。

## 3. 输入、输出与安全合同

- 普通模式缺省或空 `path` 等价于 `/`。允许根经 EvalSymlinks 解析为真实目录，跳过解析失败或非目录的根并去重；没有有效根时返回 `400 invalid_library_path`。不再提供多个允许根的列表层，从 `/` 逐级进入。
- 非空输入必须 `filepath.IsAbs(path)` 且 `path == filepath.Clean(path)`，不能先清理请求再接受。先尝试 `ValidatePath`：允许根本身和根内目录按原规则浏览。若不在根内，再判断它是否为某个允许根的真上层路径：`prefix := strings.TrimSuffix(input, "/") + "/"`，存在 `root != input && strings.HasPrefix(root, prefix)` 即成立。该段前缀比较按路径段进行，`/` 的 prefix 为 `/`，`/mu` 不会匹配 `/music`。
- 上层路径只从真实允许根路径推导下一段：对每个匹配根取 `strings.TrimPrefix(root, prefix)` 的第一段，拼成当前层 item；按 item path 去重并按 name 字节序排序。当前目录 `selectable=false`，item 仅在其 path 等于某个允许根时 `selectable=true`。上层判断只比较字符串，不读文件系统、不调用 ReadDir；不过滤隐藏目录或非 UTF-8 名称，也不套用 limit。经过符号链接的写法或不在允许根路径链上的输入不匹配，统一拒绝。
- 如果目录既在一个允许根内，又是另一个允许根的上层路径，先按根内处理，列出其子目录。
- 目录层的 `path` 为当前规范路径。仅 `/` 的 parent 为 null；其他路径的 parent 为 `filepath.Dir(path)`，`can_go_up` 等于 parent 非 null。
- 仅在允许根内调用一次 `os.ReadDir`，只保留 `entry.Type().IsDir()`、非隐藏、合法 UTF-8 名字；文件及目录符号链接不返回。当前目录和返回子项均 `selectable=true`，子项路径用当前目录与名字 Join 得到。
- 仅允许根内的子目录按 ReadDir 字节序返回，过滤后最多 2000 项，存在更多合格目录才标 truncated。上限由 ListFolders 参数传入，HTTP 固定 2000，不接受分页或客户端自定义上限。上层路径条目不套 limit。前端负责 Intl.Collator 重排，后端不实现自然排序。
- RequireAdmin 必须在列目录和查登记前执行；通过单条只读查询为当前路径和返回条目补标记，不能扫描全部登记目录或逐条查询。SQL 使用参数数组，无状态过滤；查询消费 request.Context。
- 带 `root_id` 时，缺省 path 为该登记根；path 只能等于该根或在根内，不能上行到允许根的其它区域。当前为登记根时 `parent=null`、`can_go_up=false`、`selectable=false`；根内子目录与返回子项 selectable=true。`registered`、过滤与截断规则不变，停用的登记根仍可浏览。
- 成功只向管理员暴露允许范围内的绝对路径。路径错误统一说明，不把操作系统错误原文写入响应；DB 失败不能伪装成全部未登记。

## 4. 校验与错误矩阵

| 情况 | 响应 |
| --- | --- |
| 未登录 | RequireAdmin 的 401 |
| 普通成员 | 403 forbidden |
| 相对路径、含 `..`/`.`/重复分隔符/尾斜杠的非规范路径 | 400 invalid_library_path / 打不开这个文件夹 |
| 越界、不存在、非目录、任一层符号链接或无法读取 | 同上，不泄露路径 |
| 目录内模式 root_id 非 UUID / 登记根不存在 | 400 invalid_id / 404 not_found，角色校验先于存在性 |
| 目录内模式路径在登记根外（即使仍在配置允许根内） | 400 invalid_library_path / 打不开这个文件夹 |
| 登记查询失败 | 既有安全 503 数据库错误信封，不返回部分标记 |

响应使用统一 WriteJSON/WriteError，JSON 为 `application/json`、`Cache-Control: no-store`。

## 5. 正确、基准与错误案例

- 正确：`/` 下只看到通往允许根的那一段；允许根本身的 parent 为 `/`，可选择；已登记且停用的根内目录仍标 registered。
- 正确：目录内模式停留登记根，parent 为空且不可选择；进入子目录可选择，上行最多到登记根。
- 基准：配置 `/music` 时 `/` 下只有 `music`；配置 `/home/x/music` 时 `/`、`/home`、`/home/x` 逐层只出现通往允许根的下一段。
- 错误：直接用 `input + "/"` 做前缀导致 `/` 匹配不上；对上层路径调用 ReadDir 而列出系统目录；把 `/music/a/../b` 清理后放行，或 Stat 目录符号链接后把它作为子目录返回。

## 6. 必需测试

非 DB `TestListFolders` 使用 `t.TempDir`，覆盖：① 根内子目录过滤与字节序、根内路径和条目均 selectable；② 小参数上限截断；③ 越界、非规范、不存在、普通文件、符号链接及允许根旁边目录拒绝；④ 空输入返回 `/` 层，且只包含真实允许根的第一段；⑤ 多根上层路径按 name 字节序去重、排除兄弟目录，并覆盖配置根符号链接解析。期望中的上层路径从 EvalSymlinks 后的真实根推算，不写死临时目录路径；不为验证 2000 上限创建 2000 个目录。

集成测试录制 `library-folders/root`（请求允许根这一层）、`library-folders/child` 与 `errors/invalid-library-folder`；对不带 path 的起点只断言 200、`path="/"`、`parent=null`、`selectable=false`，且唯一条目是 `realAllowedRoot` 的第一段。普通成员断言 403（不另录 forbidden）。录制 root 样本时把允许根及真实根的 parent 字符串替换为 `/music` 与 `/`；child 补齐当前及条目的 selectable，错误样本不变。样本录制不放宽禁止路径清单。

目录内模式另覆盖：鉴权先于查根、非法/未知 ID、越界到允许根内兄弟目录拒绝、根 parent/selectable、子目录导航及登记标记。

## 7. 错误与正确实现

错误：在 handler 里直接 os.ReadDir 请求参数，或先返回允许根再检查角色。
正确：先 RequireAdmin，再调用 ListFolders 严格验证和列举，最后用一条参数化查询补登记标记。已有目录新增接口仍独立校验，不把浏览结果当作后续添加操作的授权证明。
