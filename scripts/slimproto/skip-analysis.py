"""以源 PCM 为帧坐标，测量两台 Squeezelite 输出的跳帧并导出试听片段。"""

from array import array
from bisect import bisect_right
import cmath
from functools import cached_property
from importlib import import_module
import json
import math
from operator import mul
from pathlib import Path
import random
import subprocess
import sys
import wave

RATE = 44100
clock = import_module("sync-clock")
FRAME_BYTES = clock.FRAME_BYTES
SEGMENTS = ("sine", "chord", "noise")
SEGMENT_SECONDS = 31
SEGMENT_FRAMES = SEGMENT_SECONDS * RATE
FULL_SCALE = 2**31
WINDOW = 441  # 修正点前后各 220 帧，约 ±5 ms。


def generate(work, ffmpeg):
    music = work / "music"
    music.mkdir()
    marker = random.Random(22339)
    noise = random.Random(10283)
    frequencies = (220, 277.183, 329.628)
    with (work / "source.s32le").open("wb") as source:
        for name in SEGMENTS:
            for second in range(SEGMENT_SECONDS):
                pcm = array("i")
                for i in range(RATE):
                    phase = 2 * math.pi * (second + i / RATE)
                    if name == "sine":
                        level = 0.25 * math.sin(437 * phase)
                    elif name == "chord":
                        level = sum(.075 * math.sin(f * phase) + .025 * math.sin(2 * f * phase)
                                    for f in frequencies)
                    else:
                        level = noise.uniform(-.25, .25)
                    left = round(level * 2**23)
                    # 右声道最低八位叠加可复现的位置标记，使重复乐音也能按帧唯一匹配。
                    right = left + marker.randrange(-128, 128)
                    pcm.extend((left << 8, right << 8))
                if sys.byteorder == "big":
                    pcm.byteswap()
                pcm.tofile(source)

    subprocess.run([ffmpeg, "-v", "error", "-nostdin", "-f", "s32le", "-ar", str(RATE),
                    "-ac", "2", "-i", str(work / "source.s32le"), "-c:a", "flac",
                    "-bits_per_raw_sample", "24", "-metadata", "title=Skip",
                    "-metadata", "artist=ROOMusic", "-metadata", "album=SlimProto Skip Synthetic",
                    str(music / "Skip.flac")], check=True)


class Recording:
    def __init__(self, directory):
        self.name = directory.name
        self.pcm = (directory / "audio.raw").read_bytes()
        self.times, self.frames = [], []
        frame = 0
        self.ppm = int((directory / "ppm").read_text())
        for at, count, rate, ppm in clock.FRAME_SPAN.iter_unpack((directory / "frames.bin").read_bytes()):
            assert rate == RATE and ppm == self.ppm, (directory.name, rate, ppm)
            self.times.append(at)
            self.frames.append(frame)
            frame += count
        assert frame * FRAME_BYTES == len(self.pcm), (directory.name, "录音与时刻表长度不同")
        self.ns_per_frame = 1e9 / (RATE * (1 + self.ppm / 1e6))
        with (directory / "events.jsonl").open() as events:
            self.commands = [row for line in events if (row := json.loads(line))["kind"] == "correction"]

    def at_frame(self, frame):
        i = bisect_right(self.frames, frame) - 1
        return self.times[i] + round((frame - self.frames[i]) * self.ns_per_frame)

    def at_time(self, ns):
        i = bisect_right(self.times, ns) - 1
        assert i >= 0 and ns <= self.at_frame(len(self.pcm) // FRAME_BYTES - 1), "时刻超出录音范围"
        frame = self.frames[i] + round((ns - self.times[i]) / self.ns_per_frame)
        if i + 1 < len(self.frames):
            frame = min(frame, self.frames[i + 1] - 1)
        return frame

    @cached_property
    def left(self):
        return left_samples(self.pcm)

    def export(self, path, ns):
        begin = self.at_time(ns - 3_000_000_000)
        end = self.at_time(ns + 3_000_000_000)
        # 两个端点都按宿主单调钟定位；漂移设备在相同墙钟时段里会多写少量帧。
        data = self.pcm[begin * FRAME_BYTES:end * FRAME_BYTES]
        samples = len(data) // 4
        packed = bytearray(samples * 3)
        for i in range(3):
            packed[i::3] = data[i + 1::4]
        with wave.open(str(path), "wb") as wav:
            wav.setparams((2, 3, RATE, 0, "NONE", "not compressed"))
            wav.writeframes(packed)
        return begin


def left_samples(pcm):
    samples = array("i")
    samples.frombytes(pcm)
    if sys.byteorder == "big":
        samples.byteswap()
    return memoryview(samples)[::2]


def align(source, recording):
    # 用实录的双声道 PCM 与已知源逐段做字节比较，两边的位置差即丢掉的源帧数。
    # 日志只在最后配对，不参与查找断点和估计丢帧数。
    data = recording.pcm
    anchor = 16 * FRAME_BYTES
    pos = data.find(source[:anchor])
    assert pos >= 0 and pos % FRAME_BYTES == 0, (recording.name, "实录中找不到源起点")
    src = 0
    gaps = []
    while src < len(source):
        block = min(4096 * FRAME_BYTES, len(source) - src)
        if data[pos:pos + block] == source[src:src + block]:
            pos += block
            src += block
            continue
        while data[pos:pos + FRAME_BYTES] == source[src:src + FRAME_BYTES]:
            pos += FRAME_BYTES
            src += FRAME_BYTES
        pattern = data[pos:pos + anchor]
        following = source.find(pattern, src + FRAME_BYTES)
        assert len(pattern) == anchor and following >= 0 and following % FRAME_BYTES == 0, (
            recording.name, "实录不能与已知源重新对齐", pos // FRAME_BYTES, src // FRAME_BYTES)
        assert source.find(pattern, following + FRAME_BYTES) == -1, "源锚点不唯一"
        assert source[following - FRAME_BYTES:following] != data[pos - FRAME_BYTES:pos], "断点帧不唯一"
        gaps.append((pos // FRAME_BYTES, src // FRAME_BYTES, following // FRAME_BYTES))
        src = following
    assert len(gaps) == len(recording.commands), (
        recording.name, "PCM 断点数与 strm a 数不同", len(gaps), len(recording.commands))
    return gaps


class HighBand:
    def __init__(self):
        hann = [.5 - .5 * math.cos(2 * math.pi * n / (WINDOW - 1)) for n in range(WINDOW)]
        # 同一 Hann 窗及 DFT 归一化用于基线和修正点，能量比中约去共同因子。
        self.filters = [[w * cmath.exp(-2j * math.pi * k * n / WINDOW) for n, w in enumerate(hann)]
                        for k in range(math.floor(8000 * WINDOW / RATE) + 1, WINDOW // 2 + 1)]

    def energy(self, left, center):
        window = left[center - WINDOW // 2:center + WINDOW // 2 + 1].tolist()
        assert len(window) == WINDOW
        return sum(abs(sum(map(mul, window, kernel)))**2 for kernel in self.filters)


def baselines(source, band):
    left = left_samples(source)
    levels = {}
    for index, segment in enumerate(SEGMENTS):
        begin = index * SEGMENT_FRAMES
        end = begin + SEGMENT_FRAMES
        diffs = sorted(abs(a - b) for a, b in zip(left[begin:end - 1], left[begin + 1:end]))
        p99 = diffs[math.ceil(.99 * len(diffs)) - 1]
        centers = range(begin + RATE // 2, end, RATE)
        power = sum(band.energy(left, center) for center in centers) / len(centers)
        levels[segment] = (p99, power)
    return levels


def analyze(work, result):
    source = (work / "source.s32le").read_bytes()
    players = [Recording(work / name) for name in ("receiver", "member")]
    events = []
    for player in players:
        for (frame, before, after), command in zip(align(source, player), player.commands, strict=True):
            at = player.at_frame(frame)
            delay_ms = (at - command["at_ns"]) / 1e6
            assert abs(delay_ms) < 500, (player.name, "断点与指令时刻不匹配", delay_ms)
            events.append((at, player, frame, before, after, command))
    band = HighBand()
    reference = baselines(source, band)
    rows = []
    for index, (at, player, frame, before, after, command) in enumerate(sorted(events, key=lambda e: e[0]), 1):
        name = SEGMENTS[after // SEGMENT_FRAMES]
        p99, baseline_power = reference[name]
        ms = command["milliseconds"]
        dropped = after - before
        expected = ms * RATE // 1000
        assert abs(dropped - expected) <= clock.READ_FRAMES, (player.name, "丢帧数相差超过一个读取块", dropped, expected)
        jump = abs(player.left[frame] - player.left[frame - 1])
        measured_power = band.energy(player.left, frame)
        jump_db = round(20 * math.log10(jump / FULL_SCALE), 3) if jump else None
        baseline_db = round(20 * math.log10(p99 / FULL_SCALE), 3)
        excess_db = round(10 * math.log10(measured_power / baseline_power), 3)
        clips, starts = {}, {}
        for other in players:
            clips[other.name] = f"{index:02d}-{name}-{other.name}.wav"
            starts[other.name] = other.export(result / clips[other.name], at)
        rows.append(dict(segment=name, player=player.name, output_frame=frame, source_frame_before=before - 1,
                         source_frame_after=after, dropped_frames=dropped, command_ms=ms, expected_frames=expected,
                         jump_dbfs=jump_db, adjacent_p99_dbfs=baseline_db, hf_relative_db=excess_db,
                         at_ns=at, command_at_ns=command["at_ns"], wav=clips, wav_start_frame=starts))
        print(f"{index:02d} {name} {player.name} 输出帧 {frame}，strm a {ms} ms → {expected} 帧，"
              f"实丢 {dropped} 帧，跳变 {jump_db if jump else '−∞'} dBFS，段内 p99 {baseline_db} dBFS，"
              f">8 kHz 能量相对基线 {excess_db:+.3f} dB，WAV {clips['receiver']} / {clips['member']}")
    assert {row["segment"] for row in rows} == set(SEGMENTS), "三段信号未全部发生修正"
    report = dict(rate_hz=RATE, segment_seconds=SEGMENT_SECONDS, ppm={p.name: p.ppm for p in players},
                  measurement_channel="left", window_frames=WINDOW, baseline_windows_per_segment=SEGMENT_SECONDS,
                  corrections=rows)
    (result / "report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")


if __name__ == "__main__":
    mode, directory, arg = sys.argv[1:]
    if mode == "generate":
        generate(Path(directory), arg)
    elif mode == "analyze":
        analyze(Path(directory), Path(arg))
    else:
        raise SystemExit(f"未知模式：{mode}")
