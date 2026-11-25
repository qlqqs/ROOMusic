"""按设备出声采样率对 Squeezelite 的 32 位双声道 stdout 施加反压。"""

import argparse
import json
import re
import time
from pathlib import Path

RATE_CHANGE = re.compile(r"track start sample rate: (\d+)")
FRAME_BYTES = 8


def clock(directory, nominal_rate, ppm):
    real_rate = 1 + ppm / 1_000_000
    total_bytes = 0
    with (directory / "player.log").open() as player, (directory / "pcm.jsonl").open("w", buffering=1) as log:
        while True:
            with (directory / "audio.fifo").open("rb") as pipe:
                rate = nominal_rate
                deadline = time.monotonic()
                while True:
                    # 解码器可预取下一条流，只有输出线程的日志才能改变读管道的采样率。
                    for line in player:
                        changed = RATE_CHANGE.search(line)
                        if changed:
                            rate = int(changed[1])
                    data = pipe.read(rate // 50 * FRAME_BYTES)
                    if not data:
                        break
                    now = time.monotonic()
                    total_bytes += len(data)
                    frames = memoryview(data[:len(data) // FRAME_BYTES * FRAME_BYTES]).cast("Q")
                    audible = sum(frame != 0 for frame in frames)
                    # 只在有声块估算左声道频率，用于区分连续两首合成曲。
                    rises = 0
                    if audible:
                        left = memoryview(frames).cast("B").cast("i")[::2]
                        prior = left[0]
                        for current in left:
                            rises += prior <= 0 < current
                            prior = current
                    log.write(json.dumps({
                        "at_ns": time.monotonic_ns(), "rate": rate, "bytes": total_bytes,
                        "audible_frames": audible,
                        "hz": round(rises * rate / len(frames)) if audible else 0,
                    }) + "\n")
                    # 遇到读阻塞或调度延时，以实际读取时刻重新起算，避免补读突发。
                    deadline = max(deadline, now) + len(frames) / (rate * real_rate)
                    time.sleep(max(0, deadline - time.monotonic()))


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("directory", type=Path)
    parser.add_argument("--rate", type=int, default=44100)
    parser.add_argument("--ppm", type=float, default=0)
    args = parser.parse_args()
    if args.rate <= 0 or not -1_000_000 < args.ppm < 1_000_000:
        parser.error("采样率须为正数，ppm 须在 -1000000 到 1000000 之间")
    clock(args.directory, args.rate, args.ppm)
