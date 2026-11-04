# OpenCC 内置逐字字表来源

本目录保存 OpenCC 项目（BYVoid 及贡献者）的原始字表与完整 Apache-2.0 许可证。
仅作为 ROOMusic `textnorm.Version = 1` 的可复现数据基线；不声称此版本是最新版。
数据随二进制通过 `//go:embed data/opencc/*` 内置，运行期不访问网络、不链接 OpenCC，
不引入 Go binding、CGO 或额外依赖，也不加载短语/地域词汇表。

## 固定来源

- 上游仓库：`https://github.com/BYVoid/OpenCC`
- 标签：`ver.1.1.9`
- 固定提交：`556ed22496d650bd0b13b6c163be9814637970ae`
- 核实日期：2026-11-04。GitHub 官方仓库的 tag ref 指向上述 commit。
- 原始路径：`data/dictionary/TSCharacters.txt`、`data/dictionary/STCharacters.txt`、根目录 `LICENSE`。
- 原始文件按字节保留，未排序、删行、改换行或另行生成；完整 `LICENSE` 随二进制分发。
- 已检查该提交完整树；没有适用于这些字表的独立 `NOTICE`/版权文件。未复制 `deps/` 第三方代码或其许可。

| 文件 | 原始字节数 | 原始 SHA-256 |
| --- | ---: | --- |
| `TSCharacters.txt` | 34627 | `6b5a0a799bea2bb22c001f635eaa3fc2904310f0c08addbff275477a80ecf09a` |
| `STCharacters.txt` | 34353 | `ed1d268e0ad028511dcf5b0089faed0a980ad332449ec11d481ceefde6879f41` |
| `LICENSE` | 9165 | `b534e465949558eec2597b04f5092b5e161236a68dfbfd04d547592ac3964308` |

## 复现与校验

在本目录执行以下命令可重新下载同一提交的原始文件（更新版本时必须同时升级规则版本与投影）：

```bash
BASE=https://raw.githubusercontent.com/BYVoid/OpenCC/556ed22496d650bd0b13b6c163be9814637970ae
curl --fail --location "$BASE/data/dictionary/TSCharacters.txt" -o TSCharacters.txt
curl --fail --location "$BASE/data/dictionary/STCharacters.txt" -o STCharacters.txt
curl --fail --location "$BASE/LICENSE" -o LICENSE
sha256sum TSCharacters.txt STCharacters.txt LICENSE
```

从仓库 `backend/` 执行本包最小验证：

```bash
go test -race ./internal/catalog/textnorm -count=1
go vet ./internal/catalog/textnorm
```

不需要数据库、音乐目录、外部服务或运行期下载。

## 解析与选择规则

1. 检查原始 UTF-8；每行恰好一个 TAB 分开键与候选，候选用 ASCII 空格分开。
   所有键与候选都必须恰好一个非空白、非控制字符的 Unicode scalar（包括补充平面）。
   全部候选均校验，不因只选择首项而忽略后续坏项；重复键一律报错，自映射合法。
   为便于小夹具允许空行、整行 `#` 注释和 CRLF；生产原始文件仍必须通过固定哈希。
   每文件至多 1 MiB、每字表行至多 4096 字节；不对输入音乐标签偷偷截断。
2. `TSCharacters` 每行只选**第一个**简体候选，检测非自映射循环并求终值。
3. 对 `STCharacters` 的每个繁体目标，只有其**没有 TS 键**时才考虑补充。
   将所有候选简体先按 TS 终值归一；仅终值唯一时补入。冲突项保持原样，不能由遍历顺序决定。
4. 对最终有向映射再次检测循环并求终值；成功后才一次性发布不可变表。
   不计算 ST/TS 全边的无向连通分量，不把所有备选目标合并为同一个身份键。
5. 当前原始文件分别为 4113/3980 个键；ST 额外考虑 175 个 TS 未覆盖目标，其中 170 个为自映射，
   五个非自映射为 `伕→夫`、`牠→它`、`暱→昵`、`緪→縆`、`揹→背`。
   当前真实表没有 ST 补充歧义；测试以固定小夹具覆盖多个简体竞争同一目标、TS 收敛后才唯一等情况。

固定反例：`鍊→炼`、`鏈→链`，因此 `炼≠链`，不能做全边闭包。
字级歧义 `發/髮→发`、`乾/幹→干` 是已接受的宽匹配，不是基于词义的转换。

## 调用与失败边界

`LoadEmbedded() error` 用 `sync.Once` 缓存结果与错误，并校验两份字表以及许可证的哈希。
失败不发布部分表、不在 `init` 中 panic。调用方须先检查结果，阻止新投影构建/发布，保留旧搜索、目录与播放。
`Text(string) string` 和 `CatalogNumber(string) string` 只在成功预检后可调用；
未完成/失败预检后调用属于内部前置条件违规，会 panic，而不是返回伪成功的半套规范化结果。
缓存发布用原子指针；已发布字表只读，每次规范化独立创建大小写转换器。

文本顺序为 NFKC → 完整大小写折叠 → NFD 后仅移除附着于拉丁字母的组合变音符并 NFC →
规定范围片假名转平假名 → 固定繁简映射 → Unicode 空白折叠。
这里“组合变音符”限于 Unicode Mark 与 Diacritic 的交集，不顺带丢弃变体选择符或组合字符连接符；
非拉丁字母的附加符、假名浊点/半浊点均保留，不音译 `Ł`。

编号额外删除空白、Unicode `Pd` 与 U+2212，再对删除后相邻的字符按同一文本规则收敛。
例如 `a-◌́` 删除分隔符会产生新组合关系，需立即得到 `a`，不能第二次调用才改变。
普通标题/姓名不删除这些标点。两种函数均须幂等；边界 UTF-8、控制字符与长度校验由调用方负责。

测试覆盖完整嵌入文件哈希/条目数/许可证、所有真实字表键和候选的幂等性、选择反例、畸形/循环表、
并发首次加载/失败缓存、并发转换、未预检前置条件及超长标签不截断。
