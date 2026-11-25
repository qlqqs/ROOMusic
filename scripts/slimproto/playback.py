"""通过公开 API 和真实播放器输出验证 SlimProto 托管播放。"""

import http.cookiejar
import json
from pathlib import Path
import re
import secrets
import subprocess
import sys
import time
import urllib.error
import urllib.request
import uuid

PLAYBACK = "/api/v1/me/playback"
ADMIN = "/api/v1/admin/upnp"
PLAYER = "ROOMusic SlimProto Probe"
PLAYER_KEY = "slim:02:00:00:00:34:83"
SIDECAR_KEY = "slim:02:00:00:00:34:84"
SETTINGS = {
    "enabled": True, "shared": True, "upnp_enabled": False,
    "cast_enabled": False, "airplay_enabled": False, "slimproto_enabled": True,
}
TRACKS = {
    "Base": (44100, 16, 997, 16),
    "High96": (96000, 24, 997, 14),
    "High192": (192000, 24, 997, 14),
    "High352": (352800, 24, 997, 10),
    "High705": (705600, 24, 997, 10),
    "SameA": (44100, 16, 997, 6),
    "SameB": (44100, 16, 1499, 14),
}


def generate(directory, ffmpeg):
    directory.mkdir()
    for number, (title, (rate, bits, frequency, seconds)) in enumerate(TRACKS.items(), 1):
        subprocess.run([
            ffmpeg, "-v", "error", "-nostdin", "-f", "lavfi",
            "-i", f"sine=frequency={frequency}:sample_rate={rate}:duration={seconds}",
            "-ac", "2", "-c:a", "flac", "-sample_fmt", "s16" if bits == 16 else "s32",
            "-bits_per_raw_sample", str(bits), "-metadata", "album=SlimProto Synthetic",
            "-metadata", "artist=ROOMusic", "-metadata", f"track={number}",
            "-metadata", f"title={title}", str(directory / f"{number:02}.flac"),
        ], check=True)


class Client:
    def __init__(self, base):
        self.base = base
        self.http = urllib.request.build_opener(
            urllib.request.ProxyHandler({}),
            urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()),
        )

    def api(self, method, path, body=None):
        request = urllib.request.Request(
            self.base + path, method=method,
            data=json.dumps(body).encode() if body is not None else None,
            headers={"Origin": "http://app:8080", "Content-Type": "application/json",
                     "Idempotency-Key": str(uuid.uuid4())},
        )
        try:
            with self.http.open(request, timeout=5) as response:
                body = response.read()
                return json.loads(body) if body else None
        except urllib.error.HTTPError as error:
            raise RuntimeError(f"{method} {path}: HTTP {error.code}: {error.read().decode()}") from error


def until(label, read, timeout=25):
    end = time.monotonic() + timeout
    while time.monotonic() < end:
        value = read()
        if value:
            return value
        time.sleep(.1)
    raise TimeoutError(f"{label}超时（{timeout} 秒）")


def credentials(role):
    return {"username": f"slimproto-{role}", "password": secrets.token_urlsafe(24)}


def setup(base):
    admin = Client(base)
    login = credentials("admin")
    admin.api("POST", "/api/v1/setup/admin", login)
    admin.api("POST", "/api/v1/auth/login", login)
    return admin


def library(admin, expected_titles=TRACKS):
    admin.api("POST", "/api/v1/library-roots", {"path": "/music"})
    scan = admin.api("POST", "/api/v1/scans", {})

    def scanned():
        state = admin.api("GET", "/api/v1/scans/" + scan["id"])
        if state["status"] in ("failed", "canceled"):
            raise RuntimeError(f"扫描失败：{state}")
        return state["status"] == "succeeded"

    until("合成曲库扫描", scanned, 40)
    releases = admin.api("GET", "/api/v1/releases")["items"]
    tracks = {track["title"]: track["id"] for release in releases
              for medium in admin.api("GET", "/api/v1/releases/" + release["id"])["media"]
              for track in medium["tracks"]}
    assert tracks.keys() == set(expected_titles), tracks
    return tracks


def admin_device(admin, key):
    rows = admin.api("GET", ADMIN + "/devices")["items"]
    return next((row for row in rows if row["udn"] == key and row["online"]), None)


class Playback:
    def __init__(self, client, device, tracks):
        self.client = client
        self.device_id = device["device_id"]
        self.zone_id = device["zone_id"]
        self.tracks = tracks

    def device(self):
        return next((d for d in self.client.api("GET", PLAYBACK)["devices"]
                     if d["device_id"] == self.device_id), None)

    def claim(self):
        result = self.client.api("POST", PLAYBACK + f"/devices/{self.device_id}/activate", {"takeover": False})
        assert result["device"]["occupied_by"]["display_name"] == "slimproto-member", result

    def zone(self):
        return next((z for z in self.client.api("GET", PLAYBACK)["zones"] if z["zone_id"] == self.zone_id), None)

    def command(self, name, **fields):
        self.client.api("POST", PLAYBACK + f"/zones/{self.zone_id}/commands", {"command": name, **fields})

    def play(self, *titles):
        self.command("play_tracks", track_ids=[self.tracks[t] for t in titles], start_index=0)


class Receiver:
    def __init__(self, directory):
        self.path = directory / "player.log"
        self.pcm = directory / "pcm.jsonl"
        self.flac = directory / "flac.jsonl"

    def log(self, start=0):
        with self.path.open() as player:
            player.seek(start)
            return player.read()

    def mark(self):
        return self.path.stat().st_size, time.monotonic_ns()

    @staticmethod
    def records(path, after):
        rows = []
        with path.open() as stream:
            for line in stream:
                if not line.endswith("\n"):
                    break
                row = json.loads(line)
                if row["at_ns"] >= after:
                    rows.append(row)
        return rows

    def frames(self, after, rate=None, hz=None):
        return [row for row in self.records(self.pcm, after)
                if (rate is None or row["rate"] == rate)
                and (hz is None or abs(row["hz"] - hz) <= 100)]

    def seconds(self, after, rate=None, hz=None):
        return sum(row["audible_frames"] / row["rate"] for row in self.frames(after, rate, hz))

    def formats(self, after):
        return [(row["rate"], row["bits"], row["channels"]) for row in self.records(self.flac, after)]

    def expect_audio(self, mark, rate, bits, minimum=.5):
        after = mark[1]
        until(f"FLAC STREAMINFO {rate}/{bits}", lambda: (rate, bits, 2) in self.formats(after))
        try:
            return until(f"{rate} Hz 实际 PCM", lambda: heard if (
                heard := self.seconds(after, rate)) >= minimum else None)
        except TimeoutError as error:
            heard = self.seconds(after, rate)
            raise AssertionError(f"{rate} Hz 只输出 {heard:.3f}s 有声 PCM，期望至少 {minimum:g}s") from error


def run_sidecar(admin, compose):
    compose("up", "-d", "--no-deps", "squeezelite")
    sidecar = until("边车登记", lambda: row if (row := admin_device(admin, SIDECAR_KEY))
                    and row["name"] == "ROOMusic USB DAC Probe" else None)
    assert sidecar["kind"] == "slimproto" and sidecar["address"] == "127.0.0.1", sidecar
    print(f'R8a 边车 {sidecar["name"]} online=true address={sidecar["address"]}')
    compose("stop", "-t", "2", "squeezelite")


def format_limit(admin, rate):
    fmt = {"codec": "flac", "max_sample_rate": rate, "volume": "number"}
    result = admin.api("PUT", ADMIN + f"/devices/{PLAYER_KEY}/format", fmt)
    assert result == fmt, result


def broadcast_playback(work, base, compose_args):
    def compose(*args):
        subprocess.run([*compose_args, *args], check=True)

    admin = setup(base)
    tracks = library(admin)
    admin.api("PUT", ADMIN + "/settings", SETTINGS)
    run_sidecar(admin, compose)
    receiver = Receiver(work / "receiver")
    compose("up", "-d", "receiver")
    row = until("广播发现后登记", lambda: row if (row := admin_device(admin, PLAYER_KEY))
                and row["name"] == PLAYER else None)
    slim = row["capabilities"]["slimproto"]
    assert row["kind"] == "slimproto" and row["name"] == PLAYER and not row["manual"], row
    assert slim["declared_max_sample_rate"] == 705600 and not slim["unsupported"], slim
    assert row["capabilities"]["max_sample_rate"] == slim["declared_max_sample_rate"], row["capabilities"]
    assert "sending discovery" in receiver.log(), receiver.log()
    print(f'A1 广播发现 {row["name"]} firmware={slim["firmware"]} max_rate={slim["declared_max_sample_rate"]}')

    member_login = credentials("member")
    member = Client(base)
    admin.api("POST", "/api/v1/users", member_login)
    member.api("POST", "/api/v1/auth/login", member_login)
    device = until("成员可见设备", lambda: next(
        (d for d in member.api("GET", PLAYBACK)["devices"] if d.get("external_id") == PLAYER_KEY and d["online"]), None))
    player = Playback(member, device, tracks)
    player.claim()
    player.command("set_settings", settings={"leveling": "off", "crossfade": False, "gapless": True})
    member.api("PUT", f'{PLAYBACK}/devices/{device["device_id"]}/volume', {"volume": 1, "muted": False})

    mark = receiver.mark()
    player.play("Base")
    receiver.expect_audio(mark, 44100, 16, minimum=5.5)
    zone = player.zone()
    heard = receiver.seconds(mark[1], 44100)
    reported = zone["position"]["seconds"]
    difference = abs(heard - reported)
    assert zone["status"] == "playing" and difference <= 1, (zone["status"], reported, heard)
    print(f"A1 成员认领并播放 44100/16，设备回报={reported:.3f}s，有声 PCM={heard:.3f}s，差={difference:.3f}s")

    for title in ("High96", "High192"):
        rate, bits, _, _ = TRACKS[title]
        mark = receiver.mark()
        player.play(title)
        receiver.expect_audio(mark, rate, bits)
        print(f"A2 FLAC STREAMINFO {rate} Hz/{bits} bit，已输出 PCM")
    format_limit(admin, 48000)
    mark = receiver.mark()
    player.play("High192")
    receiver.expect_audio(mark, 48000, 24)
    print("A2 上限 48000 Hz，下一条流的 FLAC STREAMINFO 为 48000 Hz/24 bit")
    player.command("stop")
    format_limit(admin, None)

    mark = receiver.mark()
    player.play("SameA", "SameB")
    receiver.expect_audio(mark, 44100, 16)
    until("同格式两首的 PCM 信号", lambda: receiver.seconds(mark[1], 44100, 997) >= 5
          and receiver.seconds(mark[1], 44100, 1499) >= .5)
    first = receiver.frames(mark[1], 44100, 997)[-1]["at_ns"]
    second = receiver.frames(first, 44100, 1499)[0]["at_ns"]
    same_stream = receiver.log(mark[0])
    gap = (second - first) / 1e9
    assert same_stream.count("strm s autostart:") == 1 and receiver.formats(mark[1]) == [(44100, 16, 2)], same_stream
    assert gap <= .12, f"同采样率两首之间的相邻有声块间隔 {gap:.3f}s"
    print(f"A3 同格式 2 首只开流 1 次，曲间相邻有声块相隔 {gap:.3f}s")

    mark = receiver.mark()
    player.play("SameA", "High96")
    receiver.expect_audio(mark, 44100, 16)
    next_seq = until("准备不同采样率的接续流", lambda: (player.zone()["next_stream"] or {}).get("seq"))
    receiver.expect_audio(mark, 96000, 24)
    until("接续流晋升", lambda: (player.zone()["stream"] or {}).get("seq") == next_seq)
    mixed = receiver.log(mark[0])
    assert receiver.formats(mark[1]) == [(44100, 16, 2), (96000, 24, 2)], mixed
    assert mixed.count("strm s autostart:") == 2 and not re.search(r"connecting to|\] connected\s", mixed), mixed
    assert player.device()["occupied_by"]["display_name"] == "slimproto-member", player.device()
    print(f"A3 换采样率 44100 → 96000 Hz，接续 seq={next_seq}，播放器连接保持")

    mark = receiver.mark()
    player.command("pause")
    until("播放器确认暂停", lambda: "STAT: STMp" in receiver.log(mark[0]) and player.zone()["status"] == "paused")
    time.sleep(.25)
    quiet = time.monotonic_ns()
    time.sleep(.75)
    silence = receiver.frames(quiet)
    assert silence and receiver.seconds(quiet) < .1, "暂停期间仍有声或播放器未写出静音"
    mark = receiver.mark()
    player.command("play")
    until("播放器确认继续并输出 PCM", lambda: "STAT: STMr" in receiver.log(mark[0])
          and receiver.seconds(mark[1], 96000) >= .5)
    print("暂停与继续：STMp 后连续静音，STMr 后恢复有声 PCM")

    compose("kill", "-s", "SIGKILL", "receiver")

    def offline():
        device = player.device()
        return (device is None or not device["online"]) and admin_device(admin, PLAYER_KEY) is None

    until("杀掉播放器后设备离线", offline, 20)
    mark = receiver.mark()
    compose("start", "receiver")
    until("同一 MAC 重新上线", lambda: admin_device(admin, PLAYER_KEY))
    # 离线期间快照隐藏托管播放区。重连后、成员重新认领前检查保留的播放区。
    paused = until("原播放区再次可见", player.zone)
    assert paused["intent"] == "pause" and paused["status"] == "paused" and paused["stream"] is None, paused
    player.claim()
    player.play("Base")
    receiver.expect_audio(mark, 44100, 16)
    print("A5 SIGKILL 后设备离线且播放区暂停，同一 MAC 重启、重新认领并输出 PCM")

    # 高采样率用例放在最后，播放器卡住时仍能取得其他用例的结果。
    for title in ("High352", "High705"):
        rate, bits, _, duration = TRACKS[title]
        mark = receiver.mark()
        player.play(title)
        heard = receiver.expect_audio(mark, rate, bits, minimum=duration - .5)
        headers = receiver.records(receiver.flac, mark[1])
        assert [header["max_blocksize"] for header in headers] == [4096], headers
        header = headers[0]
        print(f'A2 FLAC STREAMINFO {header["rate"]} Hz/{header["bits"]} bit，'
              f'max_blocksize={header["max_blocksize"]}，默认缓冲输出 {heard:.3f}s 有声 PCM')


def collision_playback(base, cast_base):
    admin = setup(base)
    settings = {**SETTINGS, "upnp_enabled": True, "cast_enabled": True, "airplay_enabled": True}
    admin.api("PUT", ADMIN + "/settings", settings)
    actual = until("SlimProto 端口占用原因", lambda: (
        config if (config := admin.api("GET", ADMIN + "/settings"))["slimproto_unavailable"] == "port_in_use"
        else None))
    assert all(actual[key] == value for key, value in settings.items()), actual
    expected = {
        ("airplay", "ROOMusic-Synthetic-HomePod"),
        ("cast", "ROOMusic SlimProto Cast Probe"),
        ("upnp", "ROOMusic SlimProto UPnP Probe"),
    }
    until("SlimProto 不可用时其他协议发现设备", lambda: expected <= {
        (row["kind"], row["name"]) for row in admin.api("GET", ADMIN + "/devices")["items"] if row["online"]
    }, 40)
    tracks = library(admin)
    cast = until("SlimProto 不可用时的 Cast mDNS 发现", lambda: next(
        (d for d in admin.api("GET", PLAYBACK)["devices"]
         if d["name"] == "ROOMusic SlimProto Cast Probe" and d["online"]), None))
    player = Playback(admin, cast, tracks)
    player.play("Base")

    def receiving():
        state = Client(cast_base).api("GET", "/_fake/state")
        return state if state["player_state"] == "PLAYING" and state["bytes_read"] > 0 else None

    cast_state = until("SlimProto 不可用时 Cast 实际拉流", receiving)
    print(f'A6 TCP/UDP 3483 已占用，slimproto_unavailable=port_in_use，'
          f'UPnP、Cast、AirPlay 发现正常，Cast 拉流 {cast_state["bytes_read"]} 字节')


if __name__ == "__main__":
    mode, *args = sys.argv[1:]
    if mode == "generate":
        generate(Path(args[0]), args[1])
    elif mode == "playback":
        broadcast_playback(Path(args[0]), args[1], args[2:])
    elif mode == "collision":
        collision_playback(*args)
    else:
        raise SystemExit(f"未知测试模式：{mode}")
