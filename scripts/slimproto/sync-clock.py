"""用宿主机单调钟模拟按固定采样率消耗 32 位双声道 PCM 的声卡。"""

import argparse
from contextlib import nullcontext
import fcntl
import json
import re
import select
import struct
import time
from pathlib import Path

NANOSECOND = 1_000_000_000
FRAME_BYTES = 8
READ_FRAMES = 64
PULSE_THRESHOLD = 1 << 28
RATE_CHANGE = re.compile(r"track start sample rate: (\d+)")
INTERVAL = re.compile(r"(skip ahead|pause) interval: (\d+)")
# 每条记录保存首帧出声纳秒、帧数、名义采样率及 ppm，块内每一帧可按采样间距还原。
FRAME_SPAN = struct.Struct("<qIIi")


def capture(ppm, pipe, player, spans, emit, audio, detect_pulses):
    pipe_size = fcntl.fcntl(pipe, fcntl.F_SETPIPE_SZ, 4096)
    rate = 44100
    start = time.monotonic_ns()
    frames = pulse_frames = pulse_at = 0
    fifo_late_ns = 0
    emit("clock", start, pipe_bytes=pipe_size, ppm=ppm)

    while True:
        for line in player:
            if changed := RATE_CHANGE.search(line):
                new_rate = int(changed[1])
                if new_rate != rate:
                    start += round(frames * NANOSECOND * 1_000_000 / (rate * (1_000_000 + ppm)))
                    rate, frames = new_rate, 0
            if interval := INTERVAL.search(line):
                command = "a" if interval[1] == "skip ahead" else "p"
                milliseconds = int(interval[2])
                if command == "a" or milliseconds > 0:
                    emit("correction", time.monotonic_ns(), command=command, milliseconds=milliseconds)

        step = NANOSECOND * 1_000_000 / (rate * (1_000_000 + ppm))
        first = start + round(frames * step)
        delay = first - time.monotonic_ns()
        if delay > 0:
            time.sleep(delay / NANOSECOND)
        ready, _, _ = select.select([pipe], [], [], 0)
        wait_start = time.monotonic_ns()
        data = pipe.read(READ_FRAMES * FRAME_BYTES)
        if not data:
            return
        count, remainder = divmod(len(data), FRAME_BYTES)
        if remainder:
            raise RuntimeError(f"PCM 帧不完整（{len(data)} 字节）")
        now = time.monotonic_ns()
        # 读线程晚醒后会连续追读多块。只有在空管道上等 PCM 的时间能推迟出声。
        # 逐块扣除收到的音频时长，使持续供数过慢的短等待也能累计到欠载门限。
        if not ready:
            fifo_late_ns += now - wait_start
        fifo_late_ns = min(fifo_late_ns, max(0, now - first))
        if fifo_late_ns > 5_000_000:
            emit("underrun", now, late_ns=fifo_late_ns)
            first += fifo_late_ns
            start, frames, fifo_late_ns = first, 0, 0
        fifo_late_ns = max(0, fifo_late_ns - round(count * step))
        spans.write(FRAME_SPAN.pack(first, count, rate, ppm))

        if audio is not None:
            audio.write(data)
        if not detect_pulses:
            frames += count
            continue
        left = memoryview(data).cast("i")[::2]
        for index, sample in enumerate(left):
            if sample > PULSE_THRESHOLD:
                if not pulse_frames:
                    pulse_at = start + round((frames + index) * step)
                pulse_frames += 1
            elif pulse_frames:
                width = pulse_frames * 1000 / rate
                emit("pulse", pulse_at, rate=rate, width_ms=round(width, 3))
                pulse_frames = 0
        frames += count


def run(directory, record):
    with (directory / "player.log").open() as player, \
            (directory / "events.jsonl").open("w", buffering=1) as events, \
            (directory / "frames.bin").open("wb") as spans:
        def emit(kind, at, **fields):
            events.write(json.dumps({"kind": kind, "at_ns": at, **fields}) + "\n")

        while True:
            with (directory / "audio.fifo").open("rb", buffering=0) as pipe:
                ppm = int((directory / "ppm").read_text())
                # 同步套件同时保留漂移成员的 PCM，供逐帧计数及核对脉冲间净量。
                with ((directory / "audio.raw").open("wb") if record or ppm else nullcontext()) as audio:
                    capture(ppm, pipe, player, spans, emit, audio, detect_pulses=not record)
            spans.flush()
            if record:
                return


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("directory", type=Path)
    parser.add_argument("--record", action="store_true", help="保存一次播放的全部 PCM，在管道关闭后退出")
    args = parser.parse_args()
    run(args.directory, args.record)
