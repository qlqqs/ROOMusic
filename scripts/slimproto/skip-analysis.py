"""以源 PCM 为帧坐标，测量两台 Squeezelite 输出的逐帧补偿并导出试听片段。"""

from array import array
from bisect import bisect_right
import cmath
from functools import cached_property, lru_cache
from importlib import import_module
import json
import math
from operator import mul
from pathlib import Path
import random
from statistics import median
import struct
import subprocess
import sys
import wave

RATE = 44100
SOURCE_BITS = 24  # 合成音源的有效位深。
REFERENCE_MAX_LSB = {16: 1, 24: 4}
HARD_CORRECTION_WARMUP_SECONDS = 90
clock = import_module("sync-clock")
FRAME_BYTES = clock.FRAME_BYTES
SEGMENTS = ("sine", "chord", "noise")
SEGMENT_SECONDS = 31
SEGMENT_FRAMES = SEGMENT_SECONDS * RATE
WINDOW = 441  # 块首、中心、末端的 Hann 窗均覆盖约 ±5 ms。
PILOT = 1 << 18  # 24 位整数幅值，留出三秒最大调整块近边界处的量化余量。
MIN_BLOCK = 2000


def generate(work, ffmpeg):
    music = work / "music"
    music.mkdir()
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
                    # 四相位导频的整数样本为 ++--，分数位置处两声道差值离开 ±PILOT。
                    # 左声道完整保留听测信号，试听也只取此声道。
                    right = left + (PILOT if i % 4 < 2 else -PILOT)
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
            rows = [json.loads(line) for line in events]
        self.commands = [row for row in rows if row["kind"] == "correction"]
        self.underruns = [row for row in rows if row["kind"] == "underrun"]

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
    def samples(self):
        return pcm_samples(self.pcm)

    @property
    def left(self):
        return self.samples[::2]

    def export(self, path, ns):
        begin = self.at_time(ns - 3_000_000_000)
        end = self.at_time(ns + 3_000_000_000)
        # 两个端点都按宿主单调钟定位；漂移设备在相同墙钟时段里会多写少量帧。
        data = self.pcm[begin * FRAME_BYTES:end * FRAME_BYTES]
        samples = len(data) // FRAME_BYTES
        packed = bytearray(samples * 3)
        for i in range(3):
            packed[i::3] = data[i + 1::FRAME_BYTES]
        with wave.open(str(path), "wb") as wav:
            wav.setparams((1, 3, RATE, 0, "NONE", "not compressed"))
            wav.writeframes(packed)
        return begin


def pcm_samples(pcm):
    samples = array("i")
    samples.frombytes(pcm)
    if sys.byteorder == "big":
        samples.byteswap()
    return memoryview(samples)


def first_music_frame(pcm):
    # 两声道全零才算静音，源的首帧在右声道带位置标记。
    for frame, samples in enumerate(struct.iter_unpack("<ii", pcm)):
        if any(samples):
            return frame
    raise AssertionError("实录全为静音")


def frame_bytes(pcm, start, count=1):
    return pcm[start * FRAME_BYTES:(start + count) * FRAME_BYTES]


def music_bounds(pcm):
    # 首帧的右声道含有导频，末端按完整双声道帧上取整。
    return first_music_frame(pcm), (len(pcm.rstrip(b"\x00")) + FRAME_BYTES - 1) // FRAME_BYTES


def recorded_pauses(player):
    pauses = []
    frames = memoryview(player.pcm).cast("Q")
    for command in player.commands:
        if command["command"] != "p":
            continue
        expected = command["milliseconds"] * RATE // 1000
        # 用事件时刻限定查找范围，再核对双声道全零序列的实际帧数。
        at = player.at_time(command["at_ns"])
        lo, hi = max(0, at - RATE // 2), min(len(frames), at + RATE // 2 + expected)
        candidates = []
        i = lo
        while i < hi:
            if frames[i]:
                i += 1
                continue
            start = i
            while i < hi and not frames[i]:
                i += 1
            if i - start == expected:
                candidates.append(start)
        assert len(candidates) == 1, ("带间隔 strm p 无法唯一对应录音静音", command, candidates)
        start, = candidates
        pauses.append(dict(recorded_frame=start, frames=expected, command_ms=command["milliseconds"],
                           at_ns=player.at_frame(start), command_at_ns=command["at_ns"]))
    return pauses


def adjustment_blocks(source, pcm):
    first, end = music_bounds(pcm)
    samples = pcm_samples(pcm)

    def integer_phase(frame):
        return abs(samples[2 * frame + 1] - samples[2 * frame]) == PILOT << 8

    blocks = []
    direct_output, direct_source = first, 0
    frame = first
    while frame < end:
        if integer_phase(frame):
            frame += 1
            continue
        # 每个调整块从整数源帧起步，下一整数边界即右端。紧邻的块共享该边界。
        start = frame - 1
        assert start >= direct_output, ("调整块缺少整数起点", frame)
        direct = start - direct_output
        assert frame_bytes(pcm, direct_output, direct + 1) == frame_bytes(source, direct_source, direct + 1), (
            "块前双声道录音与源不同", direct_output, direct_source, direct)
        src = direct_source + direct
        while frame < end and not integer_phase(frame):
            frame += 1
        length = frame - start
        assert length >= MIN_BLOCK, ("意外的短调整块", start, length)
        # 导频相隔两帧符号相反，块尾的两个候选源坐标可唯一区分插、删方向。
        endpoint = frame_bytes(pcm, frame) if frame < end else b""
        deltas = [delta for delta in (-1, 1)
                  if 0 <= (src + length + delta) * FRAME_BYTES <= len(source)
                  and frame_bytes(source, src + length + delta) == endpoint]
        assert len(deltas) == 1, ("调整块末端未对齐整数源帧", start, length, src)
        delta, = deltas
        blocks.append(dict(output_frame=start, source_frame=src, length=length, delta=delta))
        direct_output, direct_source = frame, src + length + delta
    assert pcm[direct_output * FRAME_BYTES:end * FRAME_BYTES] == source[direct_source * FRAME_BYTES:], (
        "曲尾双声道录音与源不同", direct_output, direct_source)
    return first, blocks


def second_times(player, first, blocks, recorded_frame):
    # 块内对线性拉伸作逆映射，以最接近同一源整秒的输出帧计时。
    times = {}
    i = 0
    offset = first
    for second in range(SEGMENT_SECONDS * len(SEGMENTS)):
        src = second * RATE
        while i < len(blocks):
            block = blocks[i]
            if src < block["source_frame"] + block["length"] + block["delta"]:
                break
            offset -= block["delta"]
            i += 1
        frame = src + offset
        if i < len(blocks) and src >= blocks[i]["source_frame"]:
            block = blocks[i]
            frame = block["output_frame"] + round((src - block["source_frame"]) *
                                                  block["length"] / (block["length"] + block["delta"]))
        times[second] = player.at_frame(recorded_frame(frame))
    return times


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


def baselines(left, band):
    levels = {}
    for index, segment in enumerate(SEGMENTS):
        begin = index * SEGMENT_FRAMES
        end = begin + SEGMENT_FRAMES
        centers = range(begin + RATE // 2, end, RATE)
        levels[segment] = sum(band.energy(left, center) for center in centers) / len(centers)
    return levels


# I0(β√(1−u²)) 的幂级数系数；β=8 时 24 阶以后的项小于浮点精度。
I0_COEFFICIENTS = tuple(1 / math.factorial(n)**2 for n in range(24, -1, -1))


@lru_cache(maxsize=4)
def kaiser_weights(length):
    # 每个输出相位直接按 i/L 求值，不取生产实现的 1024 相位表或其线性插值。
    phases = [None]
    for i in range(1, length):
        phase = i / length
        taps = []
        for j in range(-15, 17):
            x = j - phase
            z = 16 * (1 - (x / 16)**2)
            window = 0.
            for coefficient in I0_COEFFICIENTS:
                window = window * z + coefficient
            taps.append(window * math.sin(math.pi * x) / (math.pi * x))
        gain = math.fsum(taps)
        phases.append(array("d", (w / gain for w in taps)))
    return phases


def compare_reference(block, source_channels, recorded, bits):
    length, step = block["length"], block["delta"]
    start, src = block["recorded_start_frame"], block["source_frame"]
    phases = kaiser_weights(length)
    shift = 32 - bits
    scale = 1 << shift
    full_scale = 1 << (bits - 1)
    max_error = REFERENCE_MAX_LSB[bits]
    worst, excess = -1., 0
    for i in range(length):
        if i:
            base = src + i - (step < 0)
            weights = phases[i if step > 0 else length - i]
        for channel, source in enumerate(source_channels):
            # 源两端已按首尾样本延拓，base 正好是 32 个连续抽头的左界。
            if i:
                value = math.fsum(map(mul, weights, source[base:base + 32])) / scale
                rounded = math.floor(value + .5) if value >= 0 else math.ceil(value - .5)
                expected = min(full_scale - 1, max(-full_scale, rounded))
            else:
                expected = source[src + 15] >> shift
            actual = recorded[2 * (start + i) + channel] / scale
            error = abs(actual - expected)
            excess += error > max_error
            if error > worst:
                worst = error
                position = dict(recorded_frame=start + i, block_offset=i, channel=("left", "right")[channel],
                                actual=actual, expected=expected)
    return dict(samples=length * 2, fail_samples=excess, max_error_lsb=worst, worst=position)


def measure_spectrum(block, measured, source_left, baseline, band, recorded_frame):
    source_end = len(source_left)
    start, src, length, delta = (block[k] for k in ("output_frame", "source_frame", "length", "delta"))
    span = length + delta
    windows = []
    for site, offset in (("start", 0), ("center", length // 2), ("end", length)):
        source_center = src + round(offset * span / length)
        name = SEGMENTS[min(source_center, source_end - 1) // SEGMENT_FRAMES]
        # 源的首尾用播放器的数字静音延拓。跨段窗口用同源位置作基线。
        left = source_center - WINDOW // 2
        right = left + WINDOW
        if left < 0 or right > source_end:
            padded = array("i", [0]) * max(-left, 0)
            padded.extend(source_left[max(0, left):min(right, source_end)])
            padded.extend([0] * max(0, right - source_end))
            reference_power = band.energy(memoryview(padded), WINDOW // 2)
        else:
            reference_power = band.energy(source_left, source_center)
        physical = recorded_frame(start + offset)
        measured_power = band.energy(measured, physical)
        crosses = left // SEGMENT_FRAMES != (right - 1) // SEGMENT_FRAMES
        relative_db = 10 * math.log10(measured_power / (reference_power if crosses else baseline[name]))
        windows.append(dict(site=site, segment=name, source_frame=source_center, output_frame=physical,
                            crosses_boundary=crosses, hf_relative_db=relative_db,
                            hf_matched_db=10 * math.log10(measured_power / reference_power)))
    block["windows"] = windows


def sync_statistics(values):
    ordered = sorted(values)
    return dict(sync_pairs=len(ordered), p50_ms=median(ordered),
                p95_ms=ordered[math.ceil(.95 * len(ordered)) - 1], peak_ms=ordered[-1])


def analyze(result):
    source = (result / "source.s32le").read_bytes()
    # 参考卷积需要左 15、右 16 个上下文帧，只有真实曲首曲尾使用端点延拓。
    padded = pcm_samples(source[:FRAME_BYTES] * 15 + source + source[-FRAME_BYTES:] * 16)
    source_channels = (padded[::2], padded[1::2])
    source_left = source_channels[0][15:-16]
    band = HighBand()
    baseline = baselines(source_left, band)
    players = [Recording(result / name) for name in ("receiver", "member")]
    leader, member = players
    first, end = music_bounds(leader.pcm)
    bit_perfect = frame_bytes(leader.pcm, first, end - first) == source
    commands = {player.name: {cmd: sum(row["command"] == cmd for row in player.commands) for cmd in ("a", "p")}
                for player in players}
    failures = []
    if not bit_perfect:
        failures.append("主设备未保持逐帧一致")
    origin = leader.at_frame(first)
    reference_max_lsb = REFERENCE_MAX_LSB[SOURCE_BITS]
    hard = {"before_start": [], "warmup": [], "steady": []}
    for player in players:
        for row in player.commands:
            elapsed = (row["at_ns"] - origin) / 1e9
            period = "before_start" if elapsed < 0 else (
                "warmup" if elapsed < HARD_CORRECTION_WARMUP_SECONDS else "steady")
            hard[period].append(dict(player=player.name, seconds=elapsed, **row))
    if len(hard["warmup"]) > 1 or hard["steady"]:
        failures.append(f"硬修正超出开播后前 {HARD_CORRECTION_WARMUP_SECONDS} 秒最多一次、之后零次的门限")
    report = dict(rate_hz=RATE, segment_seconds=SEGMENT_SECONDS, ppm={p.name: p.ppm for p in players},
                  underruns={p.name: p.underruns for p in players}, commands=commands, failures=failures,
                  leader_bit_perfect=bit_perfect, source_frames=len(source) // FRAME_BYTES,
                  hard_corrections=hard, playback_start_ns=origin, reference_channels=["left", "right"],
                  hard_correction_warmup_seconds=HARD_CORRECTION_WARMUP_SECONDS, source_bits=SOURCE_BITS,
                  reference_max_lsb=reference_max_lsb, measurement_channel="left", window_frames=WINDOW,
                  baseline_windows_per_segment=SEGMENT_SECONDS)

    def save():
        (result / "report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")

    print(f"主设备逐帧一致={bit_perfect}，命令={commands}，欠载（主/成员）={[len(p.underruns) for p in players]}")
    print(f"开播前/预热/稳态硬修正次数={[len(events) for events in hard.values()]}")
    try:
        pauses = recorded_pauses(member)
        report["recorded_pauses"] = pauses
        pcm = member.pcm
        for pause in reversed(pauses):
            begin = pause["recorded_frame"] * FRAME_BYTES
            pcm = pcm[:begin] + pcm[begin + pause["frames"] * FRAME_BYTES:]
        member_first, blocks = adjustment_blocks(source, pcm)
    except AssertionError as error:
        failures.append(f"成员录音无法按单帧调整块对齐，{error}")
        save()
        raise
    # 定位调整块时跨过协议静音，频谱、试听和同步均按原始录音坐标计算。
    def recorded_frame(frame):
        for pause in pauses:
            if frame >= pause["recorded_frame"]:
                frame += pause["frames"]
        return frame

    report["blocks"] = blocks
    times = second_times(member, member_first, blocks, recorded_frame)
    ref_times = second_times(leader, first, [], lambda frame: frame)
    errors = [abs(times[t] - ref_times[t]) / 1e6 for t in range(len(times))]
    if bit_perfect:
        report["sync_from_start"] = {"warmup": sync_statistics(errors[:60]), "steady": sync_statistics(errors[60:])}
    summaries = {}
    for i, name in enumerate(SEGMENTS):
        summaries[name] = dict(inserted=0, deleted=0)
        if bit_perfect:
            summaries[name].update(sync_statistics(errors[i * SEGMENT_SECONDS:(i + 1) * SEGMENT_SECONDS]))
    report["segments"] = summaries
    for block in blocks:
        start = block["output_frame"]
        block["recorded_start_frame"] = recorded_frame(start)
        block["recorded_end_frame"] = recorded_frame(start + block["length"])
        src = block["source_frame"]
        span = block["length"] + block["delta"]
        block["segment"] = SEGMENTS[(src + span // 2) // SEGMENT_FRAMES]
        excluded = []
        if recorded_frame(start + block["length"] - 1) - block["recorded_start_frame"] + 1 > block["length"]:
            excluded.append("protocol_silence")
        if src // SEGMENT_FRAMES != (src + span - 1) // SEGMENT_FRAMES:
            excluded.append("cross_segment")
        block["reference_exclusions"] = excluded
        if not excluded:
            block["reference"] = compare_reference(block, source_channels, member.samples, SOURCE_BITS)
        measure_spectrum(block, member.left, source_left, baseline, band, recorded_frame)
        summary = summaries[block["segment"]]
        summary["inserted" if block["delta"] < 0 else "deleted"] += 1

    for name, summary in summaries.items():
        centered = [b for b in blocks if b["segment"] == name]
        if not centered:
            failures.append(f"{name} 没有调整块")
            continue
        checked = [b for b in centered if "reference" in b]
        summary.update(reference_blocks=len(checked), excluded_blocks=len(centered) - len(checked),
                       protocol_silence_blocks=sum("protocol_silence" in b["reference_exclusions"] for b in centered),
                       cross_segment_blocks=sum("cross_segment" in b["reference_exclusions"] for b in centered),
                       reference_samples=sum(b["reference"]["samples"] for b in checked),
                       reference_fail_samples=sum(b["reference"]["fail_samples"] for b in checked),
                       reference_fail_blocks=sum(b["reference"]["fail_samples"] > 0 for b in checked))
        if checked:
            error_block = max(checked, key=lambda b: b["reference"]["max_error_lsb"])
            summary["max_reference_error_lsb"] = error_block["reference"]["max_error_lsb"]
            if summary["reference_fail_samples"]:
                failures.append(f"{name} 与理想参考的逐样本误差超过 {reference_max_lsb} LSB")
        else:
            error_block = centered[0]
            failures.append(f"{name} 没有可与理想参考比对的调整块")
        windows = [(w["hf_relative_db"], b, w) for b in blocks for w in b["windows"] if w["segment"] == name]
        db, hf_block, window = max(windows, key=lambda row: row[0])
        summary.update(worst_hf_db=db, worst_hf_site=window["site"], worst_hf_matched_db=window["hf_matched_db"],
                       hf_windows=len(windows),
                       hf_fail_blocks=len({b["output_frame"] for level, b, _ in windows if level > 6}))
        if db > 6:
            failures.append(f"{name} 高频能量超出 +6 dB")
        # 请求欠量为零时才会选用长于 2000 帧的前馈块。长度取整且受剩余信用影响，
        # 只报告同向相邻长块的实际净速率，不将它当成内部 SetRate 入参。
        consecutive = [b for i, b in enumerate(blocks) if i and b["segment"] == name
                       and blocks[i - 1]["segment"] == name and blocks[i - 1]["delta"] == b["delta"]
                       and blocks[i - 1]["length"] > MIN_BLOCK and b["length"] > MIN_BLOCK
                       and blocks[i - 1]["output_frame"] + blocks[i - 1]["length"] == b["output_frame"]]
        summary["consecutive_long_blocks"] = len(consecutive)
        if consecutive:
            summary["long_blocks_net_ppm"] = 1e6 * sum(b["delta"] for b in consecutive) / sum(b["length"] for b in consecutive)
        clips = []
        for block in (error_block, hf_block, centered[len(centered) // 2], centered[0], centered[-1]):
            if len(clips) >= 3:
                break
            if block["output_frame"] in clips:
                continue
            clips.append(block["output_frame"])
            at = member.at_frame(recorded_frame(block["output_frame"] + block["length"] // 2))
            block.setdefault("wav", {})
            for player in players:
                filename = f"{block['segment']}-{block['recorded_start_frame']}-{player.name}.wav"
                if player.name not in block["wav"]:
                    player.export(result / filename, at)
                    block["wav"][player.name] = filename
        print(f"{name}：{summary}")
    report["reference_totals"] = {
        key: sum(summary.get(key, 0) for summary in summaries.values())
        for key in ("reference_blocks", "excluded_blocks", "protocol_silence_blocks", "cross_segment_blocks",
                    "reference_samples", "reference_fail_samples", "reference_fail_blocks", "hf_windows")
    }
    save()
    assert not failures, failures


if __name__ == "__main__":
    mode, directory, *args = sys.argv[1:]
    if mode == "generate":
        ffmpeg, = args
        generate(Path(directory), ffmpeg)
    elif mode == "analyze":
        analyze(Path(directory))
    else:
        raise SystemExit(f"未知模式：{mode}")
