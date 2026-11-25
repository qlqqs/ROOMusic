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
SKIP = re.compile(r"skip ahead interval: (\d+)")
# 每条记录保存首帧出声纳秒、帧数、名义采样率及 ppm，块内每一帧可按采样间距还原。
FRAME_SPAN = struct.Struct("<qIIi")


def capture(directory, pipe, player, spans, emit, audio):
    pipe_size = fcntl.fcntl(pipe, fcntl.F_SETPIPE_SZ, 4096)
    ppm = int((directory / "ppm").read_text())
    rate = 44100
    start = time.monotonic_ns()
    frames = pulse_frames = pulse_at = 0
    emit("clock", start, pipe_bytes=pipe_size, ppm=ppm)

    while True:
        for line in player:
            if changed := RATE_CHANGE.search(line):
                new_rate = int(changed[1])
                if new_rate != rate:
                    start += round(frames * NANOSECOND * 1_000_000 / (rate * (1_000_000 + ppm)))
                    rate, frames = new_rate, 0
            if skip := SKIP.search(line):
                emit("correction", time.monotonic_ns(), milliseconds=int(skip[1]))

        step = NANOSECOND * 1_000_000 / (rate * (1_000_000 + ppm))
        first = start + round(frames * step)
        time.sleep(max(0, first - time.monotonic_ns()) / NANOSECOND)
        ready, _, _ = select.select([pipe], [], [], 0)
        data = pipe.read(READ_FRAMES * FRAME_BYTES)
        if not data:
            return
        count, remainder = divmod(len(data), FRAME_BYTES)
        if remainder:
            raise RuntimeError(f"PCM 帧不完整（{len(data)} 字节）")
        now = time.monotonic_ns()
        if not ready and now - first > 5_000_000:
            emit("underrun", now, late_ns=now - first)
            start, first, frames = now, now, 0
        spans.write(FRAME_SPAN.pack(first, count, rate, ppm))

        if audio is not None:
            audio.write(data)
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
            (directory / "frames.bin").open("wb") as spans, \
            ((directory / "audio.raw").open("wb") if record else nullcontext()) as audio:
        def emit(kind, at, **fields):
            events.write(json.dumps({"kind": kind, "at_ns": at, **fields}) + "\n")

        while True:
            with (directory / "audio.fifo").open("rb", buffering=0) as pipe:
                capture(directory, pipe, player, spans, emit, audio)
            spans.flush()
            if record:
                return


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("directory", type=Path)
    parser.add_argument("--record", action="store_true", help="保存一次播放的全部 PCM，在管道关闭后退出")
    args = parser.parse_args()
    run(args.directory, args.record)
