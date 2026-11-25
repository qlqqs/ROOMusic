"""用 API 编组两台真实播放器并比较计划出声时刻。"""

from bisect import bisect_left
import math
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
    def __init__(self, work, compose_args):
        self.compose_args = compose_args
        base = self.app_base()
        self.admin = setup(base)
        tracks = library(self.admin, TRACKS)
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
        if drift:
            # 留出修正指令进入输出后再采集三个完整脉冲的时间。
            time.sleep(max(0, end + 4 * SECOND - time.monotonic_ns()) / SECOND)
        else:
            time.sleep(max(0, end + PAIR_LIMIT - time.monotonic_ns()) / SECOND)
        pulses = [player.pulses(start, title) for player in self.players]
        corrections = [player.events_of("correction", start) for player in self.players]
        pairs, missing = self.compare(pulses, end)
        if not pairs:
            raise AssertionError(f"{label}无可配对脉冲，主/成员脉冲 {[len(times) for times in pulses]}")
        differences = [abs(a - b) / 1e6 for a, b in pairs]
        p50, p95, peak = statistics(differences)
        counts = [sum(row["at_ns"] < end for row in events) for events in corrections]
        print(f"{label}，{len(pairs)} 对，p50={p50:.3f} ms，p95={p95:.3f} ms，峰值={peak:.3f} ms，"
              f"strm a 主/成员={counts[0]}/{counts[1]}，未配对={sum(map(len, missing))}")
        if p95 > P95_LIMIT or peak > PEAK_LIMIT:
            worst = sorted(pairs, key=lambda pair: abs(pair[0] - pair[1]), reverse=True)[:5]
            print("最大偏差（主设备相对首脉冲秒，成员相对主设备毫秒）", [
                (round((a - initially[0][0]) / SECOND, 3), round((b - a) / 1e6, 3)) for a, b in worst])
            raise AssertionError(f"{label}超出同步阈值 p95={P95_LIMIT} ms / 峰值={PEAK_LIMIT} ms")
        assert len(pairs) >= max(1, math.ceil(seconds * .9)), (label, "有效脉冲不足", len(pairs))
        for times, commands in zip(missing, corrections):
            for at in times:
                assert drift and any(0 <= at - row["at_ns"] < PAIR_LIMIT for row in commands), (
                    label, "有脉冲缺失", (at - initially[0][0]) / SECOND)
        assert self.check_roster(self.playback.zone()) == "active", (label, self.playback.zone()["group"])
        if drift:
            self.check_recovery(end, pulses, corrections)
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

    def check_recovery(self, end, pulses, corrections):
        events = sorted(row["at_ns"] for rows in corrections for row in rows if row["at_ns"] < end)
        assert events, "+100 ppm 场景没有观察到 strm a"
        pairs, _ = self.compare(pulses, end + 4 * SECOND)
        for at in events:
            following = [abs(a - b) / 1e6 for a, b in pairs if a > at + 80_000_000][:3]
            assert len(following) == 3, "修正后的脉冲样本不足"
            p50, p95, peak = statistics(following)
            print(f"strm a 后三对脉冲，p50={p50:.3f} ms，p95={p95:.3f} ms，峰值={peak:.3f} ms")
            assert p95 <= P95_LIMIT and peak <= PEAK_LIMIT, ("strm a 修正后未恢复同步", following)

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
        self.member.ppm.write_text("100\n")
        start = time.monotonic_ns()
        self.compose("start", "member")
        self.device(SIDECAR_KEY)
        until("成员 PCM 计时器采用 +100 ppm", lambda: any(
            row["ppm"] == 100 for row in self.member.events_of("clock", start)))
        self.measure("成员 +100 ppm", self.play("Long"), duration, "Long", drift=True)


if __name__ == "__main__":
    mode, path, *args = sys.argv[1:]
    if mode == "generate":
        ffmpeg, seconds = args
        generate(Path(path), ffmpeg, int(seconds))
    elif mode == "run":
        seconds, *compose_args = args
        suite = Suite(Path(path), compose_args)
        suite.measure("无漂移持续播放", suite.play("Long"), int(seconds), "Long")
        suite.pause_and_seek()
        suite.advance("Same B", streams=1)
        suite.advance("Different", streams=2)
        suite.jitter()
        suite.reconnect()
        suite.restart_app()
        suite.drift(int(seconds))
    else:
        raise SystemExit(f"未知测试模式：{mode}")
