# 固定版本 ffmpeg 的 FLAC 最高采样率（2026-11-25 实测）

- ffmpeg：`scripts/ffmpeg-pinned.sh` 给出的 7.1.1。
- 方法：用 lavfi 正弦合成 0.2–0.5 秒立体声，`-c:a flac -sample_fmt s32` 编码，再用同一个 ffmpeg 解码成 s32le，核对字节数。
- 结果：192000、352800、384000、655350、705600、768000 都能编码，384000、705600、768000 的解码字节数与时长一致。

结论与约束：

- 655350 以上不在 FLAC 的 streamable subset 里，帧头不带采样率，只能靠 STREAMINFO。Squeezelite 用 libFLAC 解码，能不能播放 705600 和 768000 要用真实播放器验证。
- 设计 §3.2 改为：`max_sample_rate` 的封顶值暂定 768000，选项加上 705600、768000；真实播放器测试（`scripts/test-slimproto.sh`）里加一项 705600 的交付。若失败，封顶值改回 384000，并在 spec 里记录原因。
