# F4 技术设计

> 需求见 [prd.md](prd.md)。只改 `backend/internal/catalog` 与 `backend/internal/library` 两个包，没有迁移，REST 与前端不变。

## 职责划分

- **catalog**：只依据候选本身给出两样东西，不碰文件系统：
  - 哪一首曲目提供内嵌封面
  - 文件夹封面应按什么顺序查哪些目录（相对注册根）

  候选的结构（发行目录、碟序、观察的 `Directory`）由 catalog 的 organizer 决定，顺序规则也放在这里。
- **library**：拿到目录列表后，在注册根下读目录、匹配文件名并做安全检查。路径与文件系统安全一直归 library 负责。

## 接口变化

`backend/internal/catalog/persist.go`：

```go
// EmbeddedArtwork 返回按碟序、曲序第一首带内嵌封面的曲目观察；没有则 ok 为 false。
func (candidate OrganizedCandidate) EmbeddedArtwork() (SourceObservation, bool)

// ArtworkDirectories 返回查找文件夹封面的目录（相对注册根，规范化，去重）：先是发行目录 Anchor.Scope，
// 再按碟序、曲序列出各曲目观察的 Directory。CUE 虚拟轨的 Directory 是 sheet 所在目录。
func (candidate OrganizedCandidate) ArtworkDirectories() []string
```

- `ArtworkObservation()` 删除。它唯一的调用方是 `scanner.go:781`，它的“第一首曲目”后备正是缺口的来源。不保留旧名字。
- 碟序：`Mediums` 的键升序；同一碟内按 `OrganizedTrack.Position` 升序。organizer 产出的碟内曲目已经按位置排好，这里仍显式排序，不依赖这个隐含顺序。

`backend/internal/library/scanner.go`：

```go
func (service *Service) saveArtworkForRelease(ctx, executor, releaseID string, root registeredRoot, candidate catalog.OrganizedCandidate) error
func discoverFolderArtwork(rootPath string, directories []string) ([]byte, string, error)
func discoverFolderArtworkWithReader(rootPath string, directories []string, readFile func(string) ([]byte, error)) ([]byte, string, error)
```

`persistOneCandidate` 改为直接把候选交给 `saveArtworkForRelease`：先用内嵌封面，没有就按目录列表找文件夹封面，都没有就 `Remove`。
现在没有曲目的候选不会调用封面逻辑；改动后会在发行目录里找一次。organizer 不会产出没有观察的候选，所以没有可见差别。

## 文件名匹配

每个目录调用一次 `os.ReadDir`，而不是 10 次 `Lstat`。`ReadDir` 返回按文件名排序的条目，结果因此确定。

1. 对名字表（顺序不变）里的每个名字，收集 `strings.ToLower(entry.Name()) == 名字` 的条目，先放全小写的那个，其余按字节序。
2. 对每个候选文件沿用现有检查：
   - `Lstat` 确认不是符号链接、是普通文件、大小在 1 字节到 16 MiB 之间
   - `readFile` 读出内容，`artwork.SniffMIME` 能识别格式
3. 第一个通过检查的文件就是结果。没通过的记入 `firstErr`，接着找下一个。

目录本身：

- `ReadDir` 返回 `fs.ErrNotExist`：跳过这个目录，不算错误。
- 其他错误（如权限）：记入 `firstErr`，接着找下一个目录。
- 所有目录都找完仍没有结果：返回 `nil, "", firstErr`。

调用方据此决定是 `Remove`（没有错误）还是返回 `artwork_failure`（有错误）。这与现在单个目录内的语义一致，只是扩展到多个目录。

目录路径：`filepath.Join(rootPath, filepath.FromSlash(directory))`。`directory` 来自 organizer，由扫描时已验证的相对路径推导；空串表示注册根。
这与现在的做法一样，不额外引入 `isWithin` 检查。目录都是扫描遍历时真实进入过的目录，或其父目录。遍历不跟随目录符号链接，
发行目录也只会是曲目目录本身或其父目录。

## 顺序示例

| 候选 | `Anchor.Scope` | 曲目 `Directory` | 查找顺序 |
| --- | --- | --- | --- |
| 普通专辑 | `A` | `A` | `A` |
| CUE（sheet 与音频同目录） | `A` | `A`（sheet 目录） | `A` |
| CUE 跨目录（`sheets/x.cue` 引用 `../media/x.flac`） | `sheets` | `sheets` | `sheets`（不找 `media`） |
| 严格多碟 | `A` | `A/CD1`、`A/CD2` | `A` → `A/CD1` → `A/CD2` |
| Box 叶子 | `Box/CD3` | `Box/CD3` | `Box/CD3`（不找 `Box`） |
| 根目录散曲 | 空 | 空 | 注册根 |

## 测试

- **catalog 单元测试**（新增 `persist_artwork_test.go` 或并入现有测试）：
  - `ArtworkDirectories` 覆盖上表各行，另加碟号 10 排在 2 之后、去重。
  - `EmbeddedArtwork` 取碟序最前的内嵌封面；没有时返回 false。
- **library 单元测试**（`evidence_test.go`）：
  - 现有三个测试改用新签名。
  - 新增：
    - 大小写变体能被找到
    - 全小写优先
    - 优先顺序不受大小写影响
    - 前一个目录的文件无效、后一个目录有效时用后一个
    - 全部无效时返回错误
    - 目录不存在时跳过
    - 读目录失败时报告错误
- **PostgreSQL 集成测试**（`scanner_persistence_integration_test.go`）：用合成 FLAC（`parsertest`）与最小 PNG 扫描临时根目录，断言 `release_artworks` 的 `source_type` 与内容哈希：
  - CUE 专辑加 `cover.png`
  - 严格多碟专辑的父目录有 `Cover.JPG`
  - 父目录与 `CD1/` 都有封面
  - Box 叶子目录不取父目录封面
- **端到端**：用 M1b 冒烟的合成素材（`/tmp/m1b-smoke`，不入库），在 `roomusic-test` 项目里起真实后端重跑。确认 `cue-album` 有封面；
  把 `double-album` 的封面放回父目录后，也有封面。

## 回退

改动集中在两个函数和一个方法上，提交可以单独 revert。回退后，下一次扫描会把这些专辑的封面移除（回到“没有封面”），源音乐不受影响。
