# 音乐目录浏览合同

## 1. 范围与触发

新增或修改目录浏览的范围、列表、鉴权或响应时读取本合同。文件系统列举归 `library.ListFolders`，REST、鉴权与登记标记归 `library/httpapi`；不修改源目录，不递归，不改变新增扫描目录的 `ValidatePath`，不需要迁移。

## 2. 接口签名

`GET /api/v1/library-folders?path=<规范绝对路径>`，仅管理员，只读，不要求 Origin 或幂等键。

```json
{"path":"/music","parent":null,"can_go_up":false,"registered":false,"truncated":false,"items":[{"name":"Jazz","path":"/music/Jazz","registered":true}]}
```

`path`、`parent` 为字符串或 null，其余字段必传；`items` 无结果为 `[]`。`registered` 表示已登记为扫描目录，包含停用状态，不阻止浏览或选择。

## 3. 输入、输出与安全合同

- 缺省或空 `path` 是起点。配置的允许根经 EvalSymlinks 取真实目录，跳过解析失败和非目录，去重并保留配置顺序。只剩一个根直接列出其子目录，多个根返回列表层：`path/parent=null`、`can_go_up/registered=false`，条目 name 为完整路径。
- 非空输入必须 `filepath.IsAbs(path)` 且 `path == filepath.Clean(path)`，然后经 `ValidatePath`；不能先清理请求再接受它。不接受符号链接路径，也不允许越界。
- 目录层的 path 为当前规范路径。允许根本身的 parent 为 null；多个允许根时 can_go_up 为 true，表示返回列表层。根内的子目录 parent 为上一层，can_go_up 为 true。
- 单次 `os.ReadDir`，只保留 `entry.Type().IsDir()`、非隐藏、合法 UTF-8 名字；文件及目录符号链接不返回。子项路径用当前目录与名字 Join 得到。
- 子目录按 ReadDir 的字节序返回，过滤后最多 2000 项，存在更多合格目录才标 truncated。上限由 ListFolders 的参数传入，HTTP 固定 2000，不接受分页或客户端自定义上限。前端负责 Intl.Collator 重排，后端不实现自然排序。
- RequireAdmin 必须在列目录和查登记前执行；通过单条只读查询为当前路径和返回条目补标记，不能扫描全部登记目录或逐条查询。SQL 使用参数数组，无状态过滤；查询消费 request.Context。
- 成功只向管理员暴露允许范围内的绝对路径。路径错误统一说明，不把操作系统错误原文写入响应；DB 失败不能伪装成全部未登记。

## 4. 校验与错误矩阵

| 情况 | 响应 |
| --- | --- |
| 未登录 | RequireAdmin 的 401 |
| 普通成员 | 403 forbidden |
| 相对路径、含 `..`/`.`/重复分隔符/尾斜杠的非规范路径 | 400 invalid_library_path / 打不开这个文件夹 |
| 越界、不存在、非目录、任一层符号链接或无法读取 | 同上，不泄露路径 |
| 登记查询失败 | 既有安全 503 数据库错误信封，不返回部分标记 |

响应使用统一 WriteJSON/WriteError，JSON 为 `application/json`、`Cache-Control: no-store`。

## 5. 正确、基准与错误案例

- 正确：只有 `/music` 一个允许根，不传 path 直接列其子目录；已登记且停用的 `/music/Jazz` 仍标 registered。
- 基准：多个允许根的起点返回列表；进入其中一个根后 parent=null、can_go_up=true。
- 错误：把 `/music/a/../b` 清理成 `/music/b` 后放行，或 Stat 目录符号链接后把它作为子目录返回。

## 6. 必需测试

非 DB `TestListFolders` 只覆盖：① 子目录过滤与字节序；② 小参数上限截断；③ 越界、非规范、不存在、普通文件和符号链接路径拒绝；④ 单根、多根以及配置根符号链接的起点。使用 t.TempDir，不为验证 2000 上限创建 2000 个目录。

集成测试另行录 `library-folders/start`、`library-folders/child`、`errors/invalid-library-folder`，断言普通成员 403（不另录 forbidden）。样本录制前把允许根临时路径及其真实路径替换为 `/music`，不放宽禁止路径清单。

## 7. 错误与正确实现

错误：在 handler 里直接 os.ReadDir 请求参数，或先返回允许根再检查角色。
正确：先 RequireAdmin，再调用 ListFolders 严格验证和列举，最后用一条参数化查询补登记标记。已有目录新增接口仍独立校验，不把浏览结果当作后续添加操作的授权证明。
