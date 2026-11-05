# 内置拼音首字母表来源

本目录仅用于艺人浏览的首字母分组，不提供全名拼音、姓氏读音推断、中文全拼排序或拼音搜索。
运行时不联网、不读取外部文件、不新增 Go 拼音依赖。`initials.tsv` 与完整 `LICENSE` 通过
`go:embed` 随二进制分发；固定基线为 Unicode **17.0.0**，不声称是最新版或覆盖全部汉字。

## 固定上游与许可

- 数据：`https://www.unicode.org/Public/17.0.0/ucd/Unihan.zip` 中的 `Unihan_Readings.txt`。
- 版本说明：`https://www.unicode.org/Public/17.0.0/ucd/ReadMe.txt`。
- 属性说明：`https://www.unicode.org/reports/tr38/`，只使用 `kMandarin`。
- 许可原文：`https://www.unicode.org/license.txt`，Unicode License V3，按原始字节保留。
- 上游数据版权：© 2025 Unicode®, Inc.；Unicode and the Unicode Logo are registered trademarks
  of Unicode, Inc. in the U.S. and other countries. 原始数据文件头完整保留在 `manifest.json` 的
  `source_header` 字段；生成物是派生数据，不是原始 Unihan 全库。

| 对象 | SHA-256 |
| --- | --- |
| `Unihan.zip` | `f7a48b2b545acfaa77b2d607ae28747404ce02baefee16396c5d2d7a8ef34b5e` |
| `Unihan_Readings.txt` | `575e69c9ad85a4737a889a4f94cbd987042a90a1a6cc16dd3f4ed995c715b17c` |
| `LICENSE` | `e7a93b009565cfce55919a381437ac4db883e9da2126fa28b91d12732bc53d96` |
| `initials.tsv` | `8a419bf46181d6026aa6780202480461c4269266779b28f83fd499cfa045d364` |

## 生成规则与复现

生成器 `generate.py` 的格式/规则版本为 **1**，仅用 Python 3.9+ 标准库。读取并校验压缩包、
解压后的 Readings 与许可固定哈希，绝不自动下载或切换版本；只读上游输入，禁止输出覆盖输入。

1. 仅提取 `kMandarin`，每个码点只允许一条记录；取空白分隔的**第一个**读音。
2. 对该读音做 NFD，移除 Unicode Mark 声调/附加符，转大写，取首字符并要求为 A–Z。
   无法提取的项记录在错误中并中止，不静默跳过。固定输入的未知项为 **0**。
3. 输出 `U+大写十六进制码点<TAB>大写首字母<LF>`，码点至少四位、不多补零，按整数码点升序；
   无文件头、空行或时间戳。固定输入为 **44,348** 条、**416,769** 字节。
4. `manifest.json` 记录上游版本/哈希、生成器版本、输出哈希、原始文件头、多读音及未知项。
   固定输入有 **101** 条多读音记录；每项保留完整读音列表与实际选择，例如 `乾` 的 `qián gān` 选 `Q`。
   多音信息仅供审计，不用于运行期猜姓氏。生成器若遇未知项失败，不写入任何生成文件。
5. 表内容哈希也固定于 Go 加载器。更换输入、生成规则或表内容须显式更新基线并重建艺人首字母投影，
   不把数据升级伪装成原版本。

在仓库根目录复现到临时目录，再逐字节比较（下载仅发生于人工复现，不在程序启动时发生）：

```bash
DATA=backend/internal/catalog/internal/artistgraph/data/pinyin
TMP=$(mktemp -d)
curl --fail --location https://www.unicode.org/Public/17.0.0/ucd/Unihan.zip -o "$TMP/Unihan.zip"
curl --fail --location https://www.unicode.org/license.txt -o "$TMP/LICENSE"
python3 "$DATA/generate.py" --unihan "$TMP/Unihan.zip" --license "$TMP/LICENSE" --output "$TMP/generated"
for FILE in initials.tsv LICENSE manifest.json; do cmp "$DATA/$FILE" "$TMP/generated/$FILE"; done
sha256sum "$TMP/generated/initials.tsv" "$TMP/generated/LICENSE"
```

许可网址若以后变化，固定哈希校验会失败；应取得与本基线匹配的原始许可，不得未经核实自动换哈希。
Python 内置 Unicode 数据版本可能不同；当前输入涉及的拉丁声调分解稳定，逐字节比较与输出固定哈希
是复现判据，不把 Python 的本地 Unicode 版本或当前时间写进生成物。

## 运行时与错误边界

`LoadInitials() error` 用 `sync.Once` 缓存成功或失败，先验证字表与许可证哈希，再严格解析
Unicode scalar、唯一键、A–Z 单字母及码点排序，最后检查 44,348 条记录。
读取文件有 1 MiB 上限；解析拒绝非法 UTF-8、代理项、超范围码点、非规范码点、重复、倒序、
额外列与非 LF 行尾。完整校验与 `textnorm.LoadEmbedded()` 均成功后才发布不可变表；
失败不返回部分表、不在 `init` 中 panic，也不降级为所有艺人都归 `#`。

`InitialGroup(name) (string, error)` 自动预检。输入 NFKC 后取首个非空白字符，**不跳过标点**：
拉丁字母复用 `textnorm.Text` 折叠大小写与去变音符；不能投到 A–Z 的字母（如 `Ł`）归 `#`。
汉字先查本字，无读音才用既有 textnorm 明确繁简映射补查；例如 `乾` 保持 `Q`，不能先简化成 `干` 变成 `G`。
固定 Unicode 17 表直接查找，因此不因 Go Unicode 范围版本较旧而漏掉表中的新汉字。
平片假名（含半角 NFKC 后）返回 `kana`；数字、开头标点、其他文字、空白名与仍缺表者返回 `#`。
非法 UTF-8 返回错误。返回值不写入搜索索引，组内排序仍由上层按规范化名字与 ID 完成。

## 局部验证

从仓库根目录执行生成器测试；从 `backend/` 执行 Go 测试，无需数据库或音乐目录：

```bash
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s backend/internal/catalog/internal/artistgraph/data/pinyin -p 'test_*.py'
cd backend
go test -race ./internal/catalog/internal/artistgraph -run 'TestInitial|TestParseInitial|TestLoadInitial|FuzzParseInitials' -count=1
go vet ./internal/catalog/internal/artistgraph
```

测试覆盖首字母边界、Unicode 补充平面、固定首读音、日文汉字、假名、缺表繁简回退、完整嵌入数据、
坏行/坏哈希/坏许可/超大文件、并发首次加载与失败缓存，以及生成器排序、输入哈希和异常记录。
