# 本机与 CI 的媒体自动化可行性（M2a 规划用）

## 验证内容（2026-10-03，临时文件均在 `/tmp/m2a-spike`，不入库）

- 用 Python 生成两个合成 FLAC（VERBATIM 子帧、440 Hz 正弦、6 秒）：16 bit / 44.1 kHz 与 24 bit / 48 kHz。
- 临时 Go 服务用 `http.ServeContent` 直出，`Content-Type: audio/flac`、`Cache-Control: private, no-store`、强 ETag，并记录每个请求的 Range。
- Playwright 1.63 的无头 Chromium（HeadlessChrome 153，`LD_LIBRARY_PATH` 指向 `/tmp/chromelibs`）打开页面，
  调用 `canPlayType` 与 `navigator.mediaCapabilities.decodingInfo()`，再播放、拖动到 4.5 秒并等待结束。

## 结果

| 类型字符串 | `canPlayType` | `decodingInfo`（supported/smooth/powerEfficient） |
| --- | --- | --- |
| `audio/flac` | probably | true/true/true |
| `audio/mp4; codecs="alac"` | 空字符串 | false/false/false |
| `audio/mp4; codecs="mp4a.40.2"` | probably | true/true/true |
| `audio/mpeg` | probably | true/true/true |
| `audio/wav; codecs="1"` | probably | true/true/true |
| `audio/ogg; codecs="vorbis"` | probably | true/true/true |
| `audio/ogg; codecs="opus"` | probably | true/true/true |

- 两个 FLAC 都真实解码：`loadedmetadata` 时长 6 秒，播放 600 ms 后 `currentTime` 约 0.53–0.55 秒（按真实时间推进），
  拖动后 `seeked` 在 4.50 秒，最后 `ended` 在 6.00 秒；事件序列为 `playing → seeking → waiting → seeked → playing → ended`。
- 请求模式：首个请求 `Range: bytes=0-`（一次连接取完整个文件），拖动时浏览器另发 `bytes=<偏移>-`，服务端都回 206。
  说明浏览器会保持一个长连接持续读取，已建立传输的撤销上界需要单独定义（ADR 0004 §5）。
- 结论：无头 Chromium 能在本机与 CI 上驱动真实 Go 服务做“播放、拖动、结束、错误”的自动化验证；ALAC 在 Chromium 中如预期不可解码。

## Firefox 与 WebKit

- `npx playwright install firefox` 下载了 firefox-1543（在用户缓存目录 `~/.cache/ms-playwright`，不在仓库），但本机缺 GTK3、cairo、pango、
  X11-xcb 等系统库，且没有 root；照 Chromium 的做法把库解包到 `/tmp` 后才可能运行（重启后要重做）。CI 上用
  `npx playwright install --with-deps firefox` 即可安装。
- Playwright 的 WebKit 在 Linux 上不是 Safari（媒体走 GStreamer），不能作为 Safari 或 ALAC 的证据。
- ADR 0001 已批准“自动化只跑 Chromium”；若要在自动化中增加 Firefox，需要用户另行批准并修改该记录。
