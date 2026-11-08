# 目录与扫描流程验证与示例

本文件承接[目录与扫描流程合同](./library-scan-contracts.md)第5–7节，修改扫描规则与验收时共同阅读。

## 5. 正确、基准与错误案例

- 基准：重复 POST 已停用目录返回 disabled；管理员随后使用 restore 端点显式恢复。
- 正确：多碟来源按 `disc_number=1/2` 形成两个 Medium，重扫保持 Track ID。
- 正确：`sheets/album.cue` 引用 `../media/image.flac` 时，只要两者仍在同一注册根内，
  虚拟轨会与显式父来源对齐；每轨 provenance 独立，track PERFORMER 不污染相邻轨。
- 基准：历史 CUE 指向已不存在的原始 WAV，或个别 Track 没有 `INDEX 01` 时，保留
  `cue_reference` 诊断并跳过这些虚拟 Track；同目录其它有效物理来源仍可完成扫描。
- 基准：只有 EAC/XLD 明确签名位于 `.log` 前 64 KiB 时补充缺失的 CD 语义；无签名
  日志保持未知。
- 正确：目录名先补了 WEB/CD 等来源值，但同 candidate 存在明确 EAC/XLD 日志时，
  抓轨规则可以替换 `folder` 决定；tag 或 CUE 的明确来源仍保持不变。
- 正确：`Artist Beta / Artist Alpha` 作为 Track artist 被拆分后以稳定顺序展示；已登记的
  别名不再改写事实显示名，固定组合名不会因包含 `/`、`,` 或 `&` 被误拆。
- 正确：一张专辑只有一轨提供 `AlbumArtist`、其余轨只提供各自 Artist 时仍形成一个
  candidate，Release artist 与 credit 使用明确 `AlbumArtist`；缺失轨不伪造自己的 tag。
- 正确：已存在的 content-addressed 封面文件被破坏后，重扫按期望字节修复；旧 key
  清理失败返回 `artwork_failure`，不报告成功。
- 正确：CUE 专辑目录里的 `cover.png` 被绑定；严格多碟专辑父目录里的 `Cover.JPG` 被绑定；Box 叶子目录没有封面时，
  Box 父目录里的封面不会绑给它；封面后来才放进目录，下一次扫描即绑定。
- 错误：把 root 原始路径放入列表 JSON；unsupported 文件仍让扫描 succeeded 并执行 missing；把 FIFO 交给 parser；按标题删除 CUE 轨。

## 6. 必需测试

- 后端单元：终态映射、unsupported 音频候选识别、来源身份规范化和路径 containment。
- parser/organizer 单元：M4A/WAV 容器有效性、CUE 多 FILE 与跨目录 containment、
  missing reference/missing `INDEX 01` 非阻断跳过、`unsafe`/`unchecked` 引用阻断、
  sheet/track performer provenance、逐轨 map 隔离、inferred track/disc fallback、
  明确标签优先、缺失 AlbumArtist 兼容与 AlbumArtist 决策优先级、艺人/credit 多分隔符、换行/NUL、拉丁姓名与团体词、
  专辑艺人分级多值、TXXX 编码、合辑真假及严格过半、真实专辑艺人优先、原始年份回退、
  官方别名/固定组合名，rip log 64 KiB 预算、签名和仅覆盖 `folder` 的优先级，封面目录顺序（`ArtworkDirectories`：普通、CUE、
  CUE 跨目录、严格多碟含碟号 10、Box 叶子、根目录散曲、去重与按曲序）与内嵌封面选取（`EmbeddedArtwork`），
  文件夹封面发现（大小写变体、全小写优先、名字优先级不受大小写影响、多目录顺序、前一个目录无效时用后一个、目录不存在跳过、
  读目录失败报告、符号链接/无效/读取失败），以及各格式音频内容指纹（同音频不同标签
  相同、音频变化不同、FLAC MD5 为零的回退、MP3 首尾标签、OGG 注释包分页、读取中取消）。
- PostgreSQL 集成（配置 `ROOMUSIC_TEST_DATABASE_URL` 时）：用户禁用与 session
  原子性、最后管理员/并发保护、未知用户 404、目录幂等与真实状态、扫描 incomplete
  禁止 missing、多 Medium 与 Track 身份稳定；另需覆盖 staging 清理、候选短事务
  回滚/部分提交、跨目录 CUE 父来源对齐、regular-file 拒绝、身份规划（改名/移动/互换/相同音频、不完整与取消、换内容、跨 root、CUE、
  指纹复用与失败）与 Release 成员关系匹配、
  current evidence/track credits 替换、Track artist canonical 展示、受管封面内容重校验、
  旧 key 清理错误传播和绑定失败清理，封面发现（CUE 含重扫、严格多碟父目录、父目录与碟目录都有、回退到碟目录、
  Box 叶子不取父目录、内嵌优先）。

## 7. 错误与正确示例

### CUE 诊断错误与正确示例

错误：把所有 CUE diagnostic 都提升为 root incomplete；这会让缺失的历史 sidecar
引用阻断同目录中已经确定的物理音频，也无法完成真实库重扫。

```go
for range document.Diagnostics {
	complete = false
}
```

正确：诊断始终保留；只有无法证明安全边界的引用阻断扫描。`missing` 是已确定的不存在，
对应虚拟 Track 被跳过，后续成功终态可以让旧来源按正常规则进入 missing 对账。

```go
if cueReferenceMakesScanIncomplete(reference.Status) { // unsafe/unchecked/unknown
	complete = false
}
if reference.Status != "present" {
	continue
}
```

### 抓轨证据优先级错误与正确示例

错误：只看最终字符串是否为空，会把先到达的目录补充值误当作权威值。

```go
if candidate.SourceType != "" {
	return
}
candidate.SourceType = "CD"
```

正确：检查选定 decision 的 provenance；仅替换 `folder`，tag/CUE 等其它来源不变。

```go
if decision.Source == "folder" {
	candidate.SourceType = "CD"
	replaceDecision("source_type", ripLogDecision)
}
```
