# 读取外置 LRC 歌词

## Goal

曲库里很多歌词以与音频同名的 `.lrc` 文件存放、没有写进标签。扫描时读取这些文件，让歌词面板也能显示它们；音乐文件与歌词文件始终只读。

## 裁决

人工裁决（2026-11-15，用户在提问中选择）：

- 先后关系：外置优先。音频旁有可用的同名 `.lrc` 时用外置，否则用内嵌标签歌词。
- 编码：有 BOM 按 BOM（UTF-8、UTF-16LE/BE）；无 BOM 且是合法 UTF-8 按 UTF-8；否则按 GB18030 解码（用已有依赖 `golang.org/x/text`）。不做 Big5、Shift-JIS 等探测。

主代理提议、2026-11-15 用户批准最终规划摘要（依据 `research/scan-sidecar.md`）：

- 歌词署名随生效的歌词走：外置生效时去掉从内嵌歌词抽出的署名，改从 `.lrc` 开头抽取；标签署名优先不变。
- 主名匹配：精确名优先，其次 NFC 后不区分大小写且唯一；多个变体并存且无精确名时不取外置。不支持 `Song.flac.lrc`。
- 接口 `source` 取值 `embedded | external`；数据库来源键 `lrc_file`。

## Requirements

1. 按 `design.md` §2 匹配同目录 `.lrc`；只接受根内普通文件，不跟随符号链接，隐藏文件不取。
2. 按 `design.md` §3 读取（原始上限 256 KiB，不截断）与解码，再经 `parser.NormalizeLyrics`（64 KiB 上限）；任何失败回落到内嵌歌词，不记诊断、不影响曲目可用性。
3. 数据库 `lyrics_source` 新增 `lrc_file`（迁移 0050 及 `.down.sql`）；接口 `source` 增加 `external`。
4. 增量扫描发现只新增、修改、删除 `.lrc` 的情况并更新或清空歌词，音频不重新解析；外置歌词不进解析缓存，不升解析与归组版本。
5. CUE 虚拟轨不读外置歌词；逐轨抄本按物理音频文件名匹配。
6. 歌词署名按“裁决”处理。
7. 前端：无歌词文案改为“这首曲目没有歌词”；解码器接受 `source: "external"`；面板不显示来源。
8. 同步 `.trellis/spec/backend/lyrics-contracts.md`、`library-scan-incremental.md`、`library-scan-tags.md`、`.trellis/spec/frontend/player-design-guidelines.md`。

## Acceptance Criteria

- [ ] 音频旁有 UTF-8、UTF-8 BOM、UTF-16LE/BE BOM、GB18030 编码的同名 `.lrc` 时，扫描后歌词接口返回正确文本，`source` 为 `external`。
- [ ] 同时有内嵌歌词与外置 `.lrc` 时返回外置；外置文件为空、非法编码、超过上限或是符号链接时返回内嵌，`source` 为 `embedded`。
- [ ] 增量扫描：仅新增、修改、删除 `.lrc` 后再次扫描，歌词随之出现、更新、回落或清空，`FilesParsed == 0`，曲目 ID 不变。
- [ ] 外置 `.lrc` 开头的“作词：…”成为曲目署名，内嵌歌词抽出的署名不残留；标签署名仍优先。
- [ ] CUE 整轨镜像旁的 `.lrc` 不进入虚拟轨；逐轨抄本旁的 `.lrc` 生效。
- [ ] `tracks_lyrics_ck` 接受 `lrc_file`、拒绝未知来源与“有歌词无来源”；迁移往返测试通过。
- [ ] 前端解码器测试、接口样本 `tracks/lyrics-external` 与契约测试通过；歌词面板单测与 `./scripts/playback-e2e.sh` 通过。
- [ ] 扫描全过程不写音乐目录。

## Out of Scope

- 联网获取歌词、歌词编辑、逐字高亮、`.txt` 等其他歌词文件、`Song.flac.lrc`、按标题模糊匹配、Big5/Shift-JIS 探测、在面板上显示歌词来源。
