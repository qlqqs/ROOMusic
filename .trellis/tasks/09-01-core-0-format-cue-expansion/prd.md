# Core 0 扫描格式与 CUE 扩展

## Goal

在首个扫描闭环稳定后增加 OGG、Opus、WAV、常见 CUE 和多碟边界样本

## Requirements

- 前置依赖：`09-01-core-0-first-browse-slice` 已完成并固化 parser 与 source observation 边界。
- 增加 OGG、Opus 和 WAV 的基础标签与文件事实解析，复用既有确定性归一化和诊断合同。
- 支持范围明确的常见 CUE：编码与文件引用可验证、INDEX 01 可解析、虚拟 Track 可稳定重扫。
- 增加扩展多碟 fixture；不因新增 parser 改变既有 FLAC/MP3 Track 身份。
- unsupported 或超出范围的 CUE 变体必须产生明确诊断。

### 已确认的 CUE 支持子集

- 首期仅支持 UTF-8/UTF-16 文本、单个 `FILE` 音频引用、`TRACK nn AUDIO` 和
  `INDEX 01 mm:ss:ff`。
- 引用文件必须位于当前允许根内；越界、断链、多个 `FILE`、非 `AUDIO` track、
  缺失 `INDEX 01` 或无法解码的 CUE 记录为 `unsupported`/安全诊断。
- CUE 虚拟 Track 使用稳定的来源身份（CUE 相对路径 + track 编号 + 音频引用），
  重扫保持身份；不写回 CUE 或音频源文件。

## Acceptance Criteria

- [ ] OGG、Opus、WAV 代表性 fixture 形成与既有格式一致的图谱和来源。
- [ ] 支持的 CUE fixture 形成稳定虚拟 Track；重复扫描不产生重复实体。
- [ ] 损坏、越界引用和不支持的 CUE 不阻塞其他合法文件，也不触发错误 missing 对账。
- [ ] 既有 FLAC/MP3 fixture 的身份和 DTO 回归测试保持通过。

## Out of Scope

- AAC、APE、DSD、所有历史 CUE 方言和弱证据自动修复。
