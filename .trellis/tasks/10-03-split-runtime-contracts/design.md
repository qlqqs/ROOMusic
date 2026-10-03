# 运行合同拆分设计

> 需求见 [prd.md](prd.md)。行号指 2026-10-03 时的原文件（F4 实施前，713 行）；F4 会在“数据库与扫描”和矩阵里增减几行，执行时以小节为准。

## 去向

| 原内容（行号） | 去向 |
| --- | --- |
| §1 范围与触发（5–16） | 总览 §1（“见下文”改为链接）+ 新增专题文件表 |
| §2 启动方式与接口清单（20–29） | 总览 §2（接口清单保留全量，作为索引） |
| §2 用户更新、查看者响应（30–33） | identity §2 |
| §2 目录新增响应、目录列表（34–36） | library-scan §2 |
| §2 扫描状态枚举（37） | scan-coordination §2 |
| §2 前端请求形态（38–40） | 总览 §2 |
| §2 真实音乐显式验收命令（41–42） | real-library-smoke §2 |
| §2 Release 详情字段与逐轨 credits（43–46） | catalog-rest §2 |
| §3 请求与响应（52–63） | 总览 §3 |
| §3 发行列表、详情、evidence 权限与前端恢复 catalog（64–73） | catalog-rest §3 |
| §3 身份与成员接口（75–90） | identity §3 |
| §3 HTTP 缓存（92–117） | 总览 §3 |
| §3 PostgreSQL 权威与迁移执行器、0008（121–131） | 总览 §3“数据库与迁移” |
| §3 用户停用事务（132–133） | identity §3 |
| §3 目录新增与操作幂等（134–144） | library-scan §3 |
| §3 扫描完整性、迁移 0011–0014、暂存流水线、曲目与 Release 身份、CUE、parser、艺人、AlbumArtist、抓轨日志、封面（145–226） | library-scan §3 |
| §3 `canceled` 终态与 worker context（227–229） | scan-coordination §3 |
| §3 环境与资产（233–244） | 总览 §3 |
| §3 真实 Smoke 环境与 corpus 摘要（245–256） | real-library-smoke §3 |
| §3 CI 质量门禁（258–281）、HTTP 完成日志（283–305） | 总览 §3 |
| §4 矩阵：session role、非法 JSON、401、封面缓存、assets、`/api` 兜底与降级、前端非 2xx（311、316、324–328、332–334、361） | 总览 §4 |
| §4 矩阵：用户与成员（312–315、317–323） | identity §4 |
| §4 矩阵：目录幂等、扫描、CUE、staging、rip log、艺人、AlbumArtist、封面、身份规划、对账、附件（329–331、335–344、348–360） | library-scan §4 |
| §4 矩阵：V0 对照（345–347） | real-library-smoke §4 |
| §5 案例：管理员并发（365–366） | identity §5 |
| §5 案例：目录、多碟、CUE、rip log、艺人、AlbumArtist、封面（367–378、385–388） | library-scan §5 |
| §5 案例：V0 对照（379–384） | real-library-smoke §5 |
| §5 “错误”混合列表（389–391） | 按主题拆开：role → 总览；root 原始路径 → library-scan；unsupported、FIFO、按标题删 CUE 轨 → library-scan；忽略 `Exec` 错误 → identity |
| §6 测试：前端 decoder（395–396）、HTTP 中间件（413–414）、HTTP 缓存（423–428）、运行门禁（429–432） | 总览 §6 |
| §6 测试：后端单元、parser/organizer、PostgreSQL 集成（397–410） | library-scan §6 |
| §6 测试：发行 REST/前端（411–412） | catalog-rest §6 |
| §6 测试：身份与成员（415–419） | identity §6 |
| §6 测试：扫描记录（420–422） | scan-coordination §6 |
| §6 测试：真实 Smoke 比较器（433–437） | real-library-smoke §6 |
| §7 用户停用、写入失败与请求体错误（439–504） | identity §7 |
| §7 CUE 诊断、抓轨证据（506–547） | library-scan §7 |
| 场景：持久化扫描取消与跨进程协调（549–683） | scan-coordination（整节，标题层级上提一级） |
| 后续技术债（685–713） | 总览末尾（原样保留；条目里的“见上文”改为链接） |

## 预计大小

| 文件 | 约 |
| --- | --- |
| `core0-runtime-contracts.md`（总览） | 23 KB |
| `library-scan-contracts.md` | 21 KB |
| `scan-coordination-contracts.md` | 10 KB |
| `identity-contracts.md` | 8 KB |
| `real-library-smoke-contracts.md` | 4 KB |
| `catalog-rest-contracts.md`（原 9.9 KB） | 12 KB |

总览如果超过 24 KB，把后续技术债移到 `core0-tech-debt.md`，并在总览里留链接。

## 无损校验

用一次性脚本（放在 `/tmp`，不入库）：

1. 取原文件（`git show HEAD:…`）的每个非空行，跳过标题行（`#` 开头）、表格分隔行和代码围栏，规范化空白。
2. 在所有新文件与 `catalog-rest-contracts.md` 的新增部分里计数。
3. 列出出现 0 次或多于 1 次的行。

每一行不一致都要能对上 S2 允许的三类改动，并逐条写进 implement.md 的“校验记录”。

## 链接

- `backend/index.md`：层级表把“Core 0 当前运行合同”改为总览，并加入四个新文件；Quality Check 后面加一条文件大小规则。
- `database-guidelines.md:93` → `library-scan-contracts.md`
- `type-safety.md:65` → `scan-coordination-contracts.md`
- `catalog-rest-contracts.md:50` 仍指总览（HTTP 缓存留在总览）
- `frontend/index.md:52`：加上新文件
- `core-1-roadmap.md:62`：按上下文确认后再改
