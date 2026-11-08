# ⑬ 搜索忽略标点：技术设计

父任务：[扫描子系统对标](../11-08-scan-reference-port/prd.md)，T 线第 4 项，在 ⑧ 之后（⑧ 往投影加了作品字段，本项与 ⑥⑦⑧ 共用一次投影版本升级与回填）。需求见 [prd.md](./prd.md)。
参考设计只当数据读：[reference-design.md](../11-08-scan-reference-port/research/reference-design.md) 24.5（4273–4389）。

## 1. 边界与归属

| 归属 | 本项改动 |
| --- | --- |
| `catalog/textnorm` | 新增纯函数 `Loose(key string) string`；**不改** `Text`、`CatalogNumber`，**不升** `textnorm.Version` |
| `catalog/internal/searchindex` | 每个搜索值多存一个去标点键（只在与规范键不同时存） |
| `catalog/internal/artistgraph`（搜索名字） | `artist_search_names` 同样多存去标点键 |
| `catalog` 搜索 SQL | 匹配增加去标点分支，排名增加“去标点命中”的半级 |
| 迁移（合入编号 0040） | 两张搜索表加列与部分索引 |
| 前端 | 无改动（REST 形状不变） |

### 为什么不改 `Text`、不升 `textnorm.Version`

`artist_name_keys` 按 `(normalization_version, key_hash)` 查名字键（`artistgraph/names.go:53,99,108`），没有跨版本换键的迁移。升 `textnorm.Version` 会让每个名字都建新键和新艺人，**全部人工合并、改名、拆分、作用域归属与艺人收藏都失联**。去标点只服务搜索，所以放在搜索投影的第二个键里，名字键算法不动。

## 2. 数据流

```
写入（扫描候选、修正、认领、后台回填，全部走现有 searchindex / artistgraph 入口）
  original ─► normalized = Text(original) 或 CatalogNumber(original)   （不变）
           └► loose = Loose(normalized)；loose 为空或等于 normalized 时存 NULL
  写入 catalog_search_values / artist_search_names 的 loose_value、loose_grams

查询
  q ─► n = Text(q)、c = CatalogNumber(q)               （不变）
     └► ln = Loose(n)、lc = Loose(c)
  匹配 = N（现有） ∪ L1（loose_value 对 ln/lc） ∪ L2（normalized_value 对 ln/lc，仅当 ln≠n）
  排名 = 等级×2 + （只靠 L 分支命中 ? 1 : 0）
```

## 3. 接口合同

### 3.1 `textnorm.Loose`

```go
// Loose 删除规范键中的 Unicode 标点（P*）与符号（S*），再把 Unicode 空白折叠为单个空格并去首尾空白。
// 输入必须是 Text 或 CatalogNumber 的输出；不读字表、不 panic。规则变化时升搜索投影与艺人投影版本，不升 Version。
func Loose(key string) string
```

- 删除：`unicode.IsPunct(r) || unicode.IsSymbol(r)`（含 `/`、`'`、`’`、`‘`、`ʼ`、`·`、`・`、`《》`、`「」`、`、`、`〜`、`_`、`&`、`+`、`#`、`♪`、emoji）。删除时**不补空格**（`ac/dc` → `acdc`）。
- 保留：字母、数字、组合附加符、假名长音 `ー`、`々`。
- 撇号统一（PRD R2）由删除实现：`'`、`’`、`‘`、`ʼ`、`＇`（NFKC 后为 `'`）都被删除，两种写法的去标点键相同。

### 3.2 写入

- `catalog_search_values` 与 `artist_search_names` 增加 `loose_value TEXT`、`loose_grams TEXT[]`。`loose = Loose(normalized_value)`；为空或等于 `normalized_value` 时两列都为 NULL（绝大多数值没有标点，不重复存）。`loose_grams = ShortGrams(loose)`。
- 写入入口不变：`searchindex` 的单对象与批量刷新共用 `normalizeValues`，在其中顺带算 `loose`；`artistgraph/profiles.go` 写搜索名字时同样计算。
- 预算：`loose` 不会长于 `normalized_value`，沿用 65536 字节上限。

### 3.3 查询

设 `n=Text(q)`、`c=CatalogNumber(q)`、`ln=Loose(n)`、`lc=Loose(c)`。编号类字段（`catalog_number`，⑧ 之后加 `work_catalog`）用 `c`/`lc`，其余字段用 `n`/`ln`。

- **启用条件**：`ln` 非空，并且（`ln == n`，或 `ln` 去掉空格后至少 2 个字符）。例：`c#` → `ln="c"`，不启用，避免“c”匹配全库；`ac/dc` → `acdc`，启用；`acdc` → `ln==n`，启用（让它匹配到存了 `loose_value="acdc"` 的 `AC/DC`）。`lc` 同理独立判断。
- **N**：现有分支，不变。
- **L1**：`loose_value IS NOT NULL AND loose_grams @> ARRAY[gram(ln)] AND loose_value LIKE '%'||esc(ln)||'%'`。只扫有标点的值（部分索引）。
- **L2**：仅当 `ln ≠ n` 时：`short_grams @> ARRAY[gram(ln)] AND normalized_value LIKE '%'||esc(ln)||'%'`（查询带标点、值不带标点，如查 `guns n' roses` 命中 `Guns N Roses`）。
- 转义、短词 short-gram 预筛、`[[:alnum:]]{3}` 判断、普通/编号两键各自规范化，与现有合同一致，三个分支各自适用。
- **排名**：每条命中先按被比较的那个键算等级（完全相同 0、开头相同 1、包含 2），再 `rank = 等级×2 + (只由 L1/L2 命中 ? 1 : 0)`，实体取最小值。即：完全相同（原值）→ 完全相同（去标点）→ 开头相同（原值）→ 开头相同（去标点）→ 包含（原值）→ 包含（去标点）。同级内打平规则不变。
- 现有“按等级逐层取 top(offset+limit+1)”的分层策略从 3 层扩为 6 层，层间“低层排除已命中实体”的语义不变；1001 计数探测、4097 策略探测按完整匹配集合（N∪L1∪L2）进行。
- 艺人搜索（主名、别名）同样三分支、六层。
- **REST 不变**：`match.kind` 仍是 `exact|prefix|contains`（取该命中的等级，不区分是否去标点），`original` 仍是命中值的原文。

### 3.4 迁移（合入编号 0040）

```sql
ALTER TABLE catalog_search_values
  ADD COLUMN loose_value TEXT CHECK (loose_value IS NULL OR octet_length(loose_value) BETWEEN 1 AND 65536),
  ADD COLUMN loose_grams TEXT[],
  ADD CONSTRAINT catalog_search_values_loose_ck CHECK ((loose_value IS NULL) = (loose_grams IS NULL));
CREATE INDEX catalog_search_values_loose_trgm_idx ON catalog_search_values USING gin(loose_value public.gin_trgm_ops) WHERE loose_value IS NOT NULL;
CREATE INDEX catalog_search_values_loose_short_idx ON catalog_search_values USING gin(loose_grams) WHERE loose_grams IS NOT NULL;
CREATE INDEX catalog_search_values_loose_exact_idx ON catalog_search_values(md5(loose_value)) WHERE loose_value IS NOT NULL;
-- artist_search_names 同样三列约束与三个部分索引
```

迁移不回算数据：新列为 NULL，由投影版本升级后的后台回填写入。`.down.sql` 删索引与列。

### 3.5 版本与回填

| 版本 | 变化 |
| --- | --- |
| `searchindex.ProjectionVersion` | 计入 T 线的 1→2 |
| `artistgraph.Version` | O/T 整合后 3→4（搜索名字属于艺人投影） |
| `textnorm.Version` | 不变 |
| `parser.CommonVersion`、`ingest.GroupingVersion`（④ 定稿的常量） | 本项不需要（不改扫描结果，自带回填）；T 线因 ⑥⑦⑧ 合计各 +1，与本项无关 |

PRD R4：版本升级后 `maintenance.Initialize` 关闭新投影读取，旧路径继续服务；后台按清单重建全部发行、曲目与艺人搜索值；发布前搜索与艺人栏分别走 legacy 与 503 `artist_catalog_preparing`（现有协议）。不依赖扫描，部署即开始；与 ⑥⑦⑧ 同批时只回填一次。

## 4. 复刻对照表

| 参考规则（章节，行号） | 处理 | 理由 |
| --- | --- | --- |
| 24.5 默认切分 17 字符、按词建倒排（4275–4294） | 不采用 | ROOMusic 是整字段包含匹配，不分词（搜索合同） |
| 24.5.1 ASCII 折叠、德文变音双写（4296–4324） | 不采用 | 现有 `Text` 已去拉丁附加符；变音双写会改变名字键语义 |
| 24.5.2 二次拆解：`' . - *` 分隔、产出片段与“全部非分隔字符拼接串”（4326–4351） | 改写 | 只取“拼接串”这一思路：把标点与符号全部删除生成第二键（`don't` → `dont`、`rock-n-roll` → `rocknroll`）；分隔集合从 4 个扩到 Unicode P*/S*（PRD R1，含中文标点）；片段不单独产出，因为整字段包含匹配已能命中片段 |
| 24.5.2 单字符分隔词整词丢弃（4331） | 照搬（等价） | 全标点值去标点后为空，不生成第二键 |
| 24.5.2 片段长度不对称（4343–4351） | 不采用 | 不产出片段 |
| 24.5.3 CJK 二元切分（4353–4384） | 不采用 | 现有 short-gram 已覆盖一/二字检索 |
| 24.5.4 停用词与词干（4386–4389） | 不采用 | PRD 不做 |
| 24.7 检索侧目录号归一 `bwv_1050`（4516–4570） | 不采用 | 编号分支已删空白与连字符，去标点键再删点，`op67`、`bwv1050` 都能命中，不需要解析器 |

## 5. 规则输入输出对照

键生成（`Text` → `Loose`；“存储”指写入的 `loose_value`）：

| 原文 | normalized | loose | 存储 |
| --- | --- | --- | --- |
| `AC/DC` | `ac/dc` | `acdc` | `acdc` |
| `Guns N' Roses` | `guns n' roses` | `guns n roses` | `guns n roses` |
| `Guns N’ Roses` | `guns n’ roses` | `guns n roses` | `guns n roses` |
| `周杰伦·` | `周杰伦·` | `周杰伦` | `周杰伦` |
| `《夜曲》` | `《夜曲》` | `夜曲` | `夜曲` |
| `S.H.E` | `s.h.e` | `she` | `she` |
| `!!!` | `!!!` | （空） | NULL |
| `周杰伦` | `周杰伦` | `周杰伦` | NULL（与规范键相同） |
| 编号 `Op. 67`（⑧ work_catalog） | `op.67` | `op67` | `op67` |
| 编号 `SVWC-70594` | `svwc70594` | `svwc70594` | NULL |

查询命中与排名（库里同时有下列值）：

| 查询 | 命中顺序 |
| --- | --- |
| `acdc` | `ACDC`（完全相同·原值）→ `AC/DC`（完全相同·去标点）→ `ACDC Tribute`（开头·原值）→ `AC/DC Live`（开头·去标点） |
| `ac/dc` | `AC/DC`（完全相同·原值）→ `ACDC`（完全相同·去标点，L2）→ … |
| `guns n roses` | `Guns N' Roses`、`Guns N’ Roses`（完全相同·去标点） |
| `guns n' roses` | `Guns N' Roses`（完全相同·原值）→ `Guns N’ Roses`（完全相同·去标点） |
| `夜曲` | `夜曲`（原值）→ `《夜曲》`（去标点，完全相同） |
| `op67` | ⑧ 的 `Op. 67`（编号分支，去标点完全相同） |
| `c#` | 只按原值命中（去标点键只剩 1 个字符，不启用） |
| `!!!` | 只按原值命中 |

## 6. 兼容与已有数据

- 不改名字键，人工意图与收藏不受影响。
- 部署后到回填发布前，搜索走旧路径（不忽略标点）；发布后生效。不需要扫描。
- 样本库会因此能搜到的例子：`杨璐羊鹿鹿` → `杨璐（羊鹿鹿）`；`knowname` → `(K)NoW_NAME`；`青春コンプレックス` 不受影响；带 `「」`、`『』`、`【】`、`〜` 的专辑名可不带这些符号搜到。

## 7. 风险与回滚

| 风险 | 处理 |
| --- | --- |
| 查询 SQL 分支变多，热门词变慢 | L1 只走部分索引（有标点的值），L2 只在查询带标点时启用；按艺人合同 I10 只对“周杰伦”“周杰倫”的建议与三栏首屏复测一次（5 次预热/30 次测量，p95 ≤ 1 秒），不做其它性能测量 |
| 去标点后过短造成噪声 | 启用条件挡住只剩 1 个字符的情况；只靠去标点命中的结果排在同级原值之后 |
| 回填期间搜索暂时不忽略标点、艺人栏 503 | 现有协议，与 ⑥⑦⑧ 共用一次回填 |

回滚：down 迁移删列与索引；代码回退后投影版本回落，后台重建。

## 8. 裁决

已裁决（2026-11-08，见 prd.md）：R2 的“统一撇号”放进去标点键里做，不改名字键算法（`textnorm.Version` 不变）；两种撇号互相能搜到，写法不同的排在写法完全相同的之后。

## 2026-11-08 合入 main 对齐

O 线与 T 线各自已将 `artistgraph.Version` 提升到 3；整合后统一为 4，避免已有版本 3 的投影漏回填。`searchindex.ProjectionVersion=2`、`textnorm.Version=1` 保持不变。下述线内升级/回滚说明是原实施背景，合并后的回滚须连同 O 线规则判断，不能只回退版本常量。
