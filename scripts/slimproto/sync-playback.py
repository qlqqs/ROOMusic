"""用 API 编组两台真实播放器并比较计划出声时刻。"""

from bisect import bisect_left
import math
import os
from pathlib import Path
import re
from statistics import median
import subprocess
import sys
import time

from playback import ADMIN, PLAYBACK, PLAYER_KEY, SETTINGS, SIDECAR_KEY, Client, Playback, Receiver
from playback import admin_device, credentials, library, setup, until

SECOND = 1_000_000_000
P95_LIMIT = 10
PEAK_LIMIT = 15
PAIR_LIMIT = SECOND // 4
WARMUP = 60 * SECOND
UNDERRUN_WINDOW = 5 * SECOND
TRACKS = {"Long": (44100, 1, 0), "Same A": (44100, 3, 8),
          "Same B": (44100, 5, 13), "Different": (48000, 7, 13)}


def generate(directory, ffmpeg, duration):
    directory.mkdir()
    for index, (title, (rate, width, seconds)) in enumerate(TRACKS.items(), 1):
        seconds = seconds or max(duration + 45, 90)
        pulse = f"lt(mod(n+{rate * 3 // 4},{rate}),{rate * width // 1000})"
        # 底噪约为 −60 dBFS，让 FLAC 在开流的渲染领先额度内达到播放器的字节阈值。
        signal = f"aevalsrc='0.001*(2*random(0)-1)+0.7*{pulse}':s={rate}:d={seconds}"
        subprocess.run([
            ffmpeg, "-v", "error", "-nostdin", "-f", "lavfi", "-i", signal, "-ac", "2",
            "-c:a", "flac", "-sample_fmt", "s16", "-metadata", "album=SlimProto Sync Synthetic",
            "-metadata", "artist=ROOMusic", "-metadata", f"track={index}", "-metadata", f"title={title}",
            str(directory / f"{index:02}.flac"),
        ], check=True)


def nearest(at, candidates):
    index = bisect_left(candidates, at)
    nearby = candidates[max(0, index - 1):index + 1]
    return min(nearby, key=lambda candidate: abs(candidate - at), default=None)


def statistics(values):
    ordered = sorted(values)
    return median(ordered), ordered[math.ceil(.95 * len(ordered)) - 1], ordered[-1]


class ReaderUnderrun(Exception):
    """欠载后的硬修正使本次漂移验收失效。"""


class Player(Receiver):
    def __init__(self, directory):
        super().__init__(directory)
        self.events = directory / "events.jsonl"
        self.ppm = directory / "ppm"

    def pulses(self, after, title=None):
        rows = [row for row in self.records(self.events, after) if row["kind"] == "pulse"]
        if title is not None:
            rate, width, _ = TRACKS[title]
            rows = [row for row in rows if row["rate"] == rate and abs(row["width_ms"] - width) < .4]
        return [row["at_ns"] for row in rows]

    def events_of(self, kind, after):
        return [row for row in self.records(self.events, after) if row["kind"] == kind]


class Suite:
    def __init__(self, work, compose_args, titles=TRACKS):
        self.compose_args = compose_args
        base = self.app_base()
        self.admin = setup(base)
        tracks = library(self.admin, titles)
        self.admin.api("PUT", ADMIN + "/settings", SETTINGS)
        login = credentials("member")
        self.admin.api("POST", "/api/v1/users", login)
        self.client = Client(base)
        self.client.api("POST", "/api/v1/auth/login", login)
        self.leader, self.member = [Player(work / name) for name in ("receiver", "member")]
        self.players = (self.leader, self.member)
        self.compose("up", "-d", "--no-deps", "receiver", "member")
        leader = self.device(PLAYER_KEY)
        member = self.device(SIDECAR_KEY)
        self.playback = Playback(self.client, leader, tracks)
        self.member_id = member["device_id"]
        self.playback.claim()
        self.playback.command("set_settings", settings={"leveling": "off", "crossfade": False, "gapless": True})
        group = self.client.api("POST", f"{PLAYBACK}/zones/{self.playback.zone_id}/group/members",
                                {"device_id": self.member_id, "takeover": False})
        self.check_roster(group["zone"])
        for device in leader, member:
            self.client.api("PUT", f'{PLAYBACK}/devices/{device["device_id"]}/volume', {"volume": 1, "muted": False})
        print(f"已编组，主区 {self.playback.zone_id}，成员 {self.member_id}")

    def compose(self, *args):
        subprocess.run([*self.compose_args, *args], check=True)

    def app_base(self):
        published = subprocess.check_output([*self.compose_args, "port", "app", "8080"], text=True)
        return "http://127.0.0.1:" + published.strip().rsplit(":", 1)[-1]

    def device(self, key):
        return until(f"设备上线 {key}", lambda: next(
            (row for row in self.client.api("GET", PLAYBACK)["devices"]
             if row.get("external_id") == key and row["online"]), None))

    def check_roster(self, zone):
        members = (zone.get("group") or {}).get("members", [])
        assert [member["device_id"] for member in members] == [self.member_id], members
        return members[0]["state"]

    def play(self, *titles):
        start = time.monotonic_ns()
        self.playback.play(*titles)
        return start

    def measure(self, label, start, seconds, title, drift=False):
        print(f"{label}，测量 {seconds} 秒")
        try:
            initially = until(f"{label}两台的 {title} 脉冲", lambda: pulses if all(pulses := [
                player.pulses(start, title) for player in self.players]) else None, 15)
        except TimeoutError as error:
            zone = self.playback.zone()
            actual = [len(player.pulses(start, title)) for player in self.players]
            raise AssertionError(f"{error}，主/成员脉冲 {actual}，"
                                 f"播放状态 {zone['status']}，成员 {zone['group']}") from error

        end = initially[0][0] + seconds * SECOND
        time.sleep(max(0, end + PAIR_LIMIT - time.monotonic_ns()) / SECOND)
        pulses = [player.pulses(start, title) for player in self.players]
        corrections = [player.events_of("correction", start) for player in self.players]
        pairs, missing = self.compare(pulses, end)
        if not pairs and not drift:
            raise AssertionError(f"{label}无可配对脉冲，主/成员脉冲 {[len(times) for times in pulses]}")
        # 接续曲与前曲共用请求，按本曲的首个脉冲裁剪事件。
        window_start = initially[0][0] if title in ("Same B", "Different") else start
        corrections = [[row for row in events if window_start <= row["at_ns"] < end]
                       for events in corrections]
        underruns = [[row for row in player.events_of("underrun", window_start) if row["at_ns"] < end]
                     for player in self.players]
        counts = {cmd: [sum(row["command"] == cmd for row in events) for events in corrections]
                  for cmd in ("a", "p")}
        print(f"{label}，strm a 主/成员={counts['a']}，带间隔 strm p 主/成员={counts['p']}，"
              f"读端欠载 主/成员={list(map(len, underruns))}，未配对={sum(map(len, missing))}")
        # Long 的首个脉冲位于源的 0.25 秒处。
        origin = initially[0][0] - SECOND // 4
        for device, events in zip(("主设备", "成员"), underruns):
            for row in events:
                print(f"{device}读端欠载，距起播 {(row['at_ns'] - origin) / SECOND:.3f} 秒，"
                      f"迟到 {row['late_ns'] / 1e6:.3f} ms")
        if drift:
            self.check_drift(label, pairs, origin, seconds, underruns, corrections)
        else:
            p50, p95, peak = statistics([abs(a - b) / 1e6 for a, b in pairs])
            print(f"{label}，{len(pairs)} 对，p50={p50:.3f} ms，p95={p95:.3f} ms，峰值={peak:.3f} ms")
            assert p95 <= P95_LIMIT and peak <= PEAK_LIMIT, (label, "超出同步阈值", p95, peak)
        assert len(pairs) >= max(1, math.ceil(seconds * .9)), (label, "有效脉冲不足", len(pairs))
        assert not any(missing), (label, "有脉冲缺失", list(map(len, missing)))
        assert self.check_roster(self.playback.zone()) == "active", (label, self.playback.zone()["group"])
        return pairs

    @staticmethod
    def compare(pulses, end):
        # 取互为最近的脉冲，避免重复配对。只裁主设备截止点，允许对侧脉冲跨过该点。
        lead, member = pulses
        pairs = []
        paired_member = set()
        missing = ([], [])
        for at in lead:
            if at >= end:
                break
            other = nearest(at, member)
            if other is None or abs(at - other) > PAIR_LIMIT or nearest(other, lead) != at:
                missing[1].append(at)
            else:
                pairs.append((at, other))
                paired_member.add(other)
        for at in member:
            if at < end and at not in paired_member:
                other = nearest(at, lead)
                if other is None or not end <= other <= at + PAIR_LIMIT:
                    missing[0].append(at)
        return pairs, missing

    @staticmethod
    def check_drift(label, pairs, origin, seconds, underruns, corrections):
        def affected(at):
            return any(0 <= at - row["at_ns"] < UNDERRUN_WINDOW for rows in underruns for row in rows)

        clean = [(a, b) for a, b in pairs if not (affected(a) or affected(b))]
        print(f"{label}，欠载后 5 秒内排除 {len(pairs) - len(clean)}/{len(pairs)} 对")
        boundary = origin + WARMUP
        windows = [("预热", [(a, b) for a, b in clean if a < boundary], None, 15)]
        if seconds > WARMUP // SECOND:
            windows.append(("稳态", [(a, b) for a, b in clean if a >= boundary], 1, 2))

        failures = []
        for phase, samples, p95_limit, peak_limit in windows:
            if not samples:
                failures.append(f"{phase}无有效脉冲对")
                continue
            p50, p95, peak = statistics([abs(a - b) / 1e6 for a, b in samples])
            print(f"{label} {phase}，{len(samples)} 对，p50={p50:.3f} ms，"
                  f"p95={p95:.3f} ms，峰值={peak:.3f} ms")
            if peak > peak_limit or p95_limit is not None and p95 > p95_limit:
                failures.append(f"{phase}超限")
        environment, unexpected = [], []
        for device, rows in enumerate(corrections):
            for row in rows:
                # 成员读端停顿会使成员落后，主读端停顿则使成员超前。
                # 扣除两边同期的欠载，并核对它解释的修正方向与幅度。
                late = [sum(u["late_ns"] for u in events if 0 <= row["at_ns"] - u["at_ns"] < UNDERRUN_WINDOW)
                        for events in underruns]
                displacement = (late[1] - late[0]) / 1e6
                correction = row["milliseconds"] * (1 if row["command"] == "a" else -1)
                if device == 1 and correction * displacement > 0 and abs(correction - displacement) < 20:
                    environment.append(row)
                else:
                    unexpected.append(row)
        if unexpected:
            failures.append(f"未由欠载解释的硬修正 {unexpected}")
        if environment:
            print(f"欠载引发硬修正 {environment}", file=sys.stderr)
        if failures:
            print("；".join(failures), file=sys.stderr)
        if environment:
            raise ReaderUnderrun(label)
        assert not failures, (label, failures)

    def pause_and_seek(self):
        marks = [player.mark()[0] for player in self.players]
        self.playback.command("pause")
        until("两台确认暂停", lambda: self.playback.zone()["status"] == "paused" and all(
            "STAT: STMp" in player.log(mark) for player, mark in zip(self.players, marks)))
        quiet = time.monotonic_ns() + 80_000_000
        time.sleep(1.25)
        assert not any(player.pulses(quiet) for player in self.players), "暂停时仍有脉冲"
        resumed = time.monotonic_ns()
        self.playback.command("play")
        self.measure("暂停后继续", resumed, 6, "Long")

        previous = self.playback.zone()["stream"]["seq"]
        seek = time.monotonic_ns()
        self.playback.command("seek", position_seconds=20.25)
        self.measure("跳转后", seek, 6, "Long")
        zone = self.playback.zone()
        assert zone["stream"]["seq"] != previous and 20.25 <= zone["position"]["seconds"] < 30, zone["position"]

    def advance(self, title, streams):
        label = "同格式换曲" if streams == 1 else "跨采样率接续"
        marks = [player.mark()[0] for player in self.players]
        start = self.play("Same A", title)
        self.measure(label + "前", start, 5, "Same A")
        self.measure(label + "后", start, 7, title)
        for player, mark in zip(self.players, marks):
            before, after = player.pulses(start, "Same A"), player.pulses(start, title)
            gap = (after[0] - before[-1]) / SECOND
            assert .95 <= gap <= 1.05, (label, "曲间脉冲间隔", gap)
            starts = re.findall(r"strm s autostart: (\d)", player.log(mark))
            expected = ["0"] if streams == 1 else ["0", "1"]
            assert starts == expected, (label, "开流次数与 autostart", starts)
        print(f"{label}两台均保持连续脉冲，各开流 {streams} 次")

    def jitter(self):
        self.playback.command("stop")
        if os.environ.get("ROOMUSIC_SLIMPROTO_PLAYER_IMAGE"):
            self.compose("exec", "-T", "-u", "0", "member", "sh", "-ec",
                         "command -v tc >/dev/null || apk add --no-cache iproute2-tc")
        tc = ("exec", "-T", "-u", "0", "member", "tc", "qdisc")
        self.compose(*tc, "replace", "dev", "eth0", "root", "netem", "delay", "5ms", "2ms")
        try:
            self.compose(*tc, "show", "dev", "eth0")
            self.measure("成员网络 5±2 ms 抖动", self.play("Long"), 20, "Long")
        finally:
            self.compose(*tc, "del", "dev", "eth0", "root")

    def reconnect(self):
        start = self.play("Long")
        self.measure("杀掉成员前", start, 5, "Long")
        self.compose("kill", "-s", "SIGKILL", "member")
        until("成员离线", lambda: self.check_roster(self.playback.zone()) == "offline"
              and admin_device(self.admin, SIDECAR_KEY) is None)
        self.compose("start", "member")
        assert self.device(SIDECAR_KEY)["device_id"] == self.member_id
        reconnected = time.monotonic_ns()
        until("成员重连后主设备仍有脉冲", lambda: len(self.leader.pulses(reconnected, "Long")) >= 3, 6)
        assert not self.member.pulses(reconnected), "成员在下一次开流前已输出脉冲"
        lead = self.leader.pulses(start, "Long")
        gap = max(b - a for a, b in zip(lead, lead[1:])) / SECOND
        assert gap <= 1.03, f"成员掉线期间主设备脉冲最大间隔 {gap:.3f} 秒"
        print(f"成员掉线及重连期间主设备持续输出，最大脉冲间隔 {gap:.3f} 秒")
        seek = time.monotonic_ns()
        self.playback.command("seek", position_seconds=30.25)
        self.measure("重连后下一次开流", seek, 6, "Long")

    def restart_app(self):
        self.compose("restart", "-t", "2", "app")
        self.compose("up", "-d", "--wait", "app")
        # Docker 在容器重启后可能重新分配宿主机端口。
        self.admin.base = self.client.base = self.app_base()
        leader = self.device(PLAYER_KEY)
        member = self.device(SIDECAR_KEY)
        assert leader["zone_id"] == self.playback.zone_id and member["device_id"] == self.member_id
        assert leader["group"] == {"role": "leader", "zone_id": self.playback.zone_id}
        assert member["group"] == {"role": "member", "zone_id": self.playback.zone_id}
        self.check_roster(self.playback.zone())
        self.playback.claim()
        self.measure("ROOMusic 重启后原编组", self.play("Long"), 6, "Long")

    def drift(self, duration):
        self.playback.command("stop")
        self.compose("kill", "-s", "SIGKILL", "member")
        until("设置漂移前成员离线", lambda: admin_device(self.admin, SIDECAR_KEY) is None)
        ppm = int(os.environ.get("ROOMUSIC_SLIMPROTO_SYNC_PPM", "100"))
        self.member.ppm.write_text(f"{ppm}\n")
        start = time.monotonic_ns()
        self.compose("start", "member")
        self.device(SIDECAR_KEY)
        until(f"成员 PCM 计时器采用 {ppm:+d} ppm", lambda: any(
            row["ppm"] == ppm for row in self.member.events_of("clock", start)))
        self.measure(f"成员 {ppm:+d} ppm", self.play("Long"), duration, "Long", drift=True)


if __name__ == "__main__":
    mode, path, *args = sys.argv[1:]
    if mode == "generate":
        ffmpeg, seconds = args
        generate(Path(path), ffmpeg, int(seconds))
    elif mode == "record":
        suite = Suite(Path(path), args, titles=("Skip",))
        marks = [player.mark()[0] for player in suite.players]
        suite.play("Skip")
        until("两台播完测量曲目", lambda: all("STAT: STMu" in player.log(mark)
                                      for player, mark in zip(suite.players, marks)), 180)
    elif mode == "run":
        seconds, *compose_args = args
        suite = Suite(Path(path), compose_args)
        scenarios = [
            lambda: suite.measure("无漂移持续播放", suite.play("Long"), int(seconds), "Long"),
            suite.pause_and_seek,
            lambda: suite.advance("Same B", streams=1),
            lambda: suite.advance("Different", streams=2),
            suite.jitter, suite.reconnect, suite.restart_app,
            lambda: suite.drift(int(seconds)),
        ]
        failures = []
        environmental = []
        for scenario in scenarios:
            try:
                scenario()
            except ReaderUnderrun as error:
                environmental.append(str(error))
            except (AssertionError, TimeoutError) as error:
                print(f"场景失败：{error}", file=sys.stderr)
                failures.append(str(error))
        if environmental:
            print(f"环境失败（读端欠载触发硬修正）：{environmental}", file=sys.stderr)
            raise SystemExit(3)
        if failures:
            raise AssertionError(failures)
    else:
        raise SystemExit(f"未知测试模式：{mode}")
