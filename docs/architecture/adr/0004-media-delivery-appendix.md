# ADR 0004 附录：外部资料与核验范围

本附录是 [ADR 0004](0004-media-delivery.md) 的原第 12 节，于 2026-10-15 原样移出，使正文不超过 Trellis 给子代理注入文件的上限（32,768 字节）。正文括号里的 M01–M15 对应下表编号。

## 12. 外部资料与核验范围

以下均在 **2026-09-27** 查阅。编号对应正文论点；资料不构成本项目已兼容、已完成部署或合规的
证明。FFmpeg 主线文档/源码会变化，最终必须核对所选构建对应的版本文档与许可证。

| 编号 | 官方/原始资料 | 本文使用的证据 |
| --- | --- | --- |
| M01 | MDN：`https://developer.mozilla.org/en-US/docs/Web/Media/Guides/Formats/Containers` | 容器与 codec 不同；选择组合而非仅凭扩展名判断 |
| M02 | MDN：`https://developer.mozilla.org/en-US/docs/Web/API/HTMLMediaElement/canPlayType` | 空字符串、`maybe`、`probably` 的能力含义，不是具体文件播放保证 |
| M03 | W3C：`https://www.w3.org/TR/media-capabilities/` | `decodingInfo()` 与 supported/smooth/powerEfficient；可用性与具体配置需验证 |
| M04 | WHATWG HTML：`https://html.spec.whatwg.org/multipage/media.html#dom-media-play` | 媒体播放 Promise、允许播放与拒绝处理；不能用 UI 状态代替真实播放 |
| M05 | Go：`https://pkg.go.dev/net/http#ServeContent` | Range/条件请求、可定位内容要求及应用设置 Content-Type/ETag 的责任 |
| M06 | FFmpeg：`https://ffmpeg.org/ffmpeg.html` | streamcopy 与转码不同；输入 seek、accurate seek 和 streamcopy 的取舍 |
| M07 | FFprobe：`https://ffmpeg.org/ffprobe.html` | 查询容器/流信息的能力，不是自动可信或自动有界的探测器 |
| M08 | FFmpeg filters：`https://ffmpeg.org/ffmpeg-filters.html#atrim` | 可按样本裁剪；裁剪不自动重置时间戳 |
| M09 | FFmpeg formats：`https://ffmpeg.org/ffmpeg-formats.html#Fragmentation` | 普通与分片 MOV/MP4 的完成/可解码性差别；输出方式需要独立验收 |
| M10 | RFC 9110：`https://www.rfc-editor.org/rfc/rfc9110.html#section-14` | Range、Content-Range、If-Range、部分响应与不可满足范围语义 |
| M11 | RFC 9111：`https://www.rfc-editor.org/rfc/rfc9111.html#section-5.2.2` | private/no-store 与缓存层边界；不是删除已交付内容的机制 |
| M12 | Go：`https://pkg.go.dev/os/exec` | 不自动使用 shell、CommandContext 与取消/等待相关机制 |
| M13 | FFmpeg protocols：`https://ffmpeg.org/ffmpeg-protocols.html#Protocol-Options` | protocol_whitelist 限制允许协议，不能替代进程/文件沙箱 |
| M14 | FFmpeg：`https://ffmpeg.org/legal.html`；原始许可说明：`https://raw.githubusercontent.com/FFmpeg/FFmpeg/master/LICENSE.md` | LGPL/GPL 与构建配置有关；nonfree 再分发限制；官方合规检查提示与专利问题边界 |
| M15 | W3C Web Audio API，固定 Recommendation（2021-06-17）：`https://www.w3.org/TR/2021/REC-webaudio-20210617/#dom-audiocontext-audiocontext` | §1.2.1 构造器第 4 步的第 2 子步骤：AudioContext 与输出设备采样率不同时需要重采样，不能据此声称端到端 Bit-perfect |
