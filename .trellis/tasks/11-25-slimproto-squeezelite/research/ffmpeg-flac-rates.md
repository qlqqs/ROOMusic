# 固定版本 ffmpeg 的 FLAC 最高采样率（2026-11-25 实测）

- ffmpeg：`scripts/ffmpeg-pinned.sh` 给出的 7.1.1。
- 方法：用 lavfi 正弦合成 0.2–0.5 秒立体声，`-c:a flac -sample_fmt s32` 编码，再用同一个 ffmpeg 解码成 s32le，核对字节数。
- 结果：192000、352800、384000、655350、705600、768000 都能编码，384000、705600、768000 的解码字节数与时长一致。

结论与约束：

- 655350 以上不在 FLAC 的 streamable subset 里，帧头不带采样率，只能靠 STREAMINFO。Squeezelite 用 libFLAC 解码，能不能播放 705600 和 768000 要用真实播放器验证。
- 设计 §3.2 改为：`max_sample_rate` 的封顶值暂定 768000，选项加上 705600、768000；真实播放器测试（`scripts/test-slimproto.sh`）里加一项 705600 的交付。若失败，封顶值改回 384000，并在 spec 里记录原因。

## 真实播放器结果（2026-11-25，`scripts/test-slimproto.sh`）

- 播放器：Squeezelite 2.0.0（从固定上游提交构建的 `squeezelite-2.0.0_p1613-r0`）。
- 96 kHz/24 与 192 kHz/24 正常。
- 705.6 kHz/24：FLAC 头正确，但在 32 MiB 输出缓冲下出声片刻后 PCM 与状态回报同时停止。源码显示该版本为 FLAC 解码预留 200 KiB，此采样率的单个解码块要 256 KiB。改用 64 MiB 缓冲后能播完。
- 第一次结论（已推翻）：封顶 384000。改为 384000 后重测，352.8 kHz 同样卡住：问题不在采样率，而在块大小。
- 块大小实测（ffmpeg 7.1.1，STREAMINFO）：默认 44.1k 为 4608、192k 为 16384、352.8k 与 705.6k 为 32768 帧；加 `-frame_size 4096` 后都为 4096。32768 帧双声道解码为 256 KiB，超过 Squeezelite 为 FLAC 预留的 200 KiB；4096 帧只要 32 KiB。
- 最终结论：渲染的 FLAC 编码在输出高于 48 kHz 时固定 `-frame_size 4096`，SlimProto 上限恢复为 768000。无条件固定曾让已有测试 `TestRenderSilentFLACStartsWithinRenderWindow/8000Hz_16bit_1ch` 失败（8 kHz 单声道静音每块攒 0.5 秒、整段不足 32 KiB），所以只对高采样率生效。
- 真实播放器重测（默认缓冲）：352.8 kHz 与 705.6 kHz 均以原采样率交付，STREAMINFO 最大块 4096 帧，10 秒曲目读到 9.5 秒以上有声 PCM。
