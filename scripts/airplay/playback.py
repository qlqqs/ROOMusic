"""从播放区命令入口验证接收端实际写出的 PCM。"""

import array
import http.cookiejar
import json
import math
import os
from pathlib import Path
import secrets
import struct
import subprocess
import sys
import time
import urllib.error
import urllib.request
import uuid

RATE = 44100
PERIOD = 98
PEAK = 12000
CAPTURE = Path("/audio.pcm")
BASE = "http://app:8080"
PLAYBACK = "/api/v1/me/playback"
ORIGIN_HEADERS = {"Origin": BASE, "Content-Type": "application/json"}
def session():
    return urllib.request.build_opener(
        urllib.request.ProxyHandler({}),
        urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()),
    )


client = session()


def wave_frame(track, frame):
    phase = 2 * math.pi * track * (frame % PERIOD) / PERIOD
    return round(PEAK * math.sin(phase)), round(PEAK * math.cos(phase))


def generate(directory, ffmpeg):
    directory = Path(directory)
    directory.mkdir()
    for track, seconds in [(1, 5), (2, 60)]:
        cycle = b"".join(struct.pack("<hh", *wave_frame(track, n)) for n in range(PERIOD))
        subprocess.run([
            ffmpeg, "-v", "error", "-f", "s16le", "-ar", str(RATE), "-ac", "2",
            "-i", "pipe:0", "-c:a", "flac", "-metadata", "album=AirPlay PCM",
            "-metadata", "artist=ROOMusic", "-metadata", f"title=Tone {track}",
            "-metadata", f"track={track}", str(directory / f"0{track}.flac"),
        ], input=cycle * (seconds * RATE // PERIOD), check=True)


def api(method, path, body=None, expected=None, http=client):
    request = urllib.request.Request(
        BASE + path, method=method,
        data=json.dumps(body).encode() if body is not None else None,
        headers={**ORIGIN_HEADERS, "Idempotency-Key": str(uuid.uuid4())},
    )
    try:
        with http.open(request, timeout=12) as response:
            if expected is not None and response.status != expected:
                raise RuntimeError(f"{method} {path}: HTTP {response.status}, 期待 {expected}")
            data = response.read()
            return json.loads(data) if data else None
    except urllib.error.HTTPError as error:
        raise RuntimeError(f"{method} {path}: HTTP {error.code}: {error.read().decode()}") from error


def until(label, read, timeout=15):
    end = time.monotonic() + timeout
    while time.monotonic() < end:
        value = read()
        if value:
            return value
        time.sleep(0.1)
    raise TimeoutError(f"{label} 超时，管道已输出 {CAPTURE.stat().st_size // 4} 帧")


def prepare_library():
    credentials = {"username": "airplay-probe", "password": secrets.token_urlsafe(24)}
    api("POST", "/api/v1/setup/admin", credentials)
    api("POST", "/api/v1/auth/login", credentials)
    api("POST", "/api/v1/library-roots", {"path": "/music"})
    scan = api("POST", "/api/v1/scans", {})
    until("扫描", lambda: api("GET", "/api/v1/scans/" + scan["id"])["status"] == "succeeded")
    releases = api("GET", "/api/v1/releases")["items"]
    tracks = [t for release in releases
              for medium in api("GET", "/api/v1/releases/" + release["id"])["media"]
              for t in medium["tracks"]]
    assert len(tracks) == 2, tracks
    return [t["id"] for t in sorted(tracks, key=lambda t: t["position"])]


def samples(data):
    result = array.array("h")
    result.frombytes(data)
    if sys.byteorder != "little":
        result.byteswap()
    return result


def tone(samples_, track):
    # I/Q 双声道的相位差固定，旋转一个周期后即可分别检验频率、左右声道及幅度。
    size = len(samples_) // 2
    if size == 0:
        return 0, 0
    re = im = energy = 0
    period = PERIOD // track
    cycle = [complex(*wave_frame(track, n)).conjugate() / PEAK for n in range(period)]
    for i in range(size):
        value = complex(samples_[2*i], samples_[2*i+1])
        aligned = value * cycle[i % period]
        re += aligned.real
        im += aligned.imag
        energy += value.real**2 + value.imag**2
    amplitude = math.hypot(re, im) / size
    return amplitude, amplitude / math.sqrt(energy / size) if energy else 0


def recent(match, after, size=2048):
    with CAPTURE.open("rb") as stream:
        end = stream.seek(0, os.SEEK_END) // 4
        if end < after + size:
            return None
        stream.seek((end - size) * 4)
        return match(samples(stream.read(size * 4)))


def at_volume(track, low, high):
    def match(data):
        amplitude, correlation = tone(data, track)
        if low <= amplitude <= high and correlation > .995:
            return amplitude
        return None
    return match


def check_boundary():
    data = samples(CAPTURE.read_bytes())
    block = PERIOD * 4
    found_first = False
    for i in range(0, len(data) // 2 - block, block):
        part = data[i*2:(i+block)*2]
        if at_volume(1, .95 * PEAK, 1.05 * PEAK)(part):
            found_first = True
        if found_first and at_volume(2, .95 * PEAK, 1.05 * PEAK)(part):
            break
    else:
        raise AssertionError("管道未检出两个连续的全音量合成信号")

    # 候选切点的前后各取两个周期，逐样本对齐源中曲一的结尾和曲二的开头。
    width = PERIOD * 2
    source = [sample for x in range(-width, width)
              for sample in wave_frame(1 if x < 0 else 2, x)]
    best_error, best_frame = float("inf"), 0
    for pos in range(max(width, i - 2 * block), min(i + block, len(data)//2 - width)):
        if abs(data[pos*2]) > PEAK / 6 or data[pos*2+1] < .9 * PEAK:
            continue
        actual = data[(pos-width)*2:(pos+width)*2]
        error = math.sqrt(sum((a-b)**2 for a, b in zip(actual, source)) / len(source))
        if error < best_error:
            best_error, best_frame = error, pos
    assert best_error < PEAK * .03, f"曲间逐样本误差过大：{best_error:.1f} / {PEAK}"
    print(f"FIFO cross-track at frame={best_frame} stereo RMS error={best_error:.2f}/{PEAK}")


def production_controls(tracks):
    def homepod():
        items = api("GET", "/api/v1/admin/upnp/devices")["items"]
        return next((r for r in items if r["name"] == "ROOMusic-Synthetic-HomePod"), None)
    row = until("合成 HomePod 公告", homepod)
    info = row["capabilities"]["airplay"]
    assert info["ptp_required"] and info["timing_reason"] == "port_in_use", info
    assert info["unsupported"] and "精确对时不可用" in info["unavailable_reason"], info
    credentials = {"username": "airplay-listener", "password": secrets.token_urlsafe(24)}
    member = session()
    api("POST", "/api/v1/users", credentials, expected=201)
    api("POST", "/api/v1/auth/login", credentials, http=member)
    visible = api("GET", PLAYBACK, http=member)["devices"]
    blocked = next(r for r in visible if r.get("external_id") == row["udn"])
    assert blocked["unavailable_reason"] == info["unavailable_reason"] == "精确对时不可用", blocked
    try:
        api("POST", PLAYBACK + "/devices/" + blocked["device_id"] + "/activate", {"takeover": False}, http=member)
    except RuntimeError as error:
        if not all(token in str(error) for token in ("HTTP 409", "device_unsupported", "精确对时不可用")):
            raise
    else:
        raise AssertionError("PTP 不可用时仍可取得该设备的会话")
    print("PTP-only model:", row["model"], info["timing_reason"], blocked["unavailable_reason"],
          "member activate=409 device_unsupported")
    api("PUT", "/api/v1/admin/upnp/settings", {"cast_enabled": True})
    def cast():
        devices = api("GET", PLAYBACK)["devices"]
        return next((d for d in devices if d["name"] == "ROOMusic-Synthetic-Cast" and d["online"]), None)
    device = until("Cast mDNS 发现", cast, 40)
    cmd = f'{PLAYBACK}/zones/{device["zone_id"]}/commands'
    api("POST", cmd, {"command": "play_tracks", "track_ids": tracks, "start_index": 0})
    def cast_state():
        with client.open("http://cast:8099/_fake/state", timeout=3) as response:
            return json.load(response)
    def playing():
        state = cast_state()
        return state if state["player_state"] == "PLAYING" and state["bytes_read"] > 0 else None
    state = until("Cast 接收端实际拉流", playing)
    api("POST", cmd, {"command": "pause"})
    until("Cast 接收端暂停", lambda: cast_state()["player_state"] == "PAUSED")
    print(f'Cast discovery + LOAD bytes={state["bytes_read"]} PAUSE=PAUSED')


def playback(mode, name, clock=""):
    tracks = prepare_library()
    api("PUT", "/api/v1/admin/upnp/settings", {
        "enabled": True, "shared": True, "upnp_enabled": False,
        "cast_enabled": False, "airplay_enabled": True,
    })

    def device():
        return next((d for d in api("GET", PLAYBACK)["devices"]
                     if d["name"] == name and d["online"]), None)

    d = until("播放区发现", device, 40)
    zone_id = d["zone_id"]
    admin_path = "/api/v1/admin/upnp/devices/" + d["external_id"]
    listed = api("GET", "/api/v1/admin/upnp/devices")
    print("admin devices:", json.dumps(listed, ensure_ascii=False))
    row = next(row for row in listed["items"] if row["udn"] == d["external_id"])
    if clock in ("ptp", "occupied", "lost"):
        info = row["capabilities"]["airplay"]
        assert info["timing_protocol"] == ("NTP" if clock == "occupied" else "PTP"), info
        if clock == "occupied":
            assert info["timing_reason"] == "port_in_use", info
    ip = row["address"].split(":")[0]
    added = api("POST", "/api/v1/admin/upnp/devices", {"kind": "airplay", "address": ip})
    assert added["manual"] and added["udn"] == d["external_id"], added
    print("admin manual:", json.dumps(added, ensure_ascii=False))
    fmt = {"codec": "alac", "max_sample_rate": 44100, "max_bit_depth": 16}
    if mode == "airplay2":
        high = {**fmt, "max_sample_rate": 48000, "max_bit_depth": 24}
        actual = api("PUT", admin_path + "/format", high)
        assert high == actual, actual
        print("admin 48/24:", json.dumps(actual))
    actual_format = api("PUT", admin_path + "/format", fmt)
    assert actual_format == fmt, actual_format
    print("admin format:", json.dumps(actual_format))

    def zone():
        return next(z for z in api("GET", PLAYBACK)["zones"] if z["zone_id"] == zone_id)

    def command(action, **params):
        return api("POST", f"{PLAYBACK}/zones/{zone_id}/commands", {"command": action, **params})

    def volume(level):
        api("PUT", f'{PLAYBACK}/devices/{d["device_id"]}/volume', {"volume": level, "muted": False})

    command("set_settings", settings={"leveling": "off", "crossfade": False, "gapless": True})
    volume(1)
    command("play_tracks", track_ids=tracks, start_index=0)
    peak = until("首曲全音量", lambda: recent(at_volume(1, .95 * PEAK, 1.05 * PEAK), 0), 18)
    stream = zone()["stream"]
    assert stream["url"] == "" and stream["mime_type"] == "audio/alac", stream
    assert stream["format"] == {"codec": "alac", "sample_rate": RATE, "bit_depth": 16, "channels": 2}, stream["format"]
    if clock == "lost":
        first_frames = CAPTURE.stat().st_size // 4
        Path("/first-tone").write_text("PCM\n")

        def fallback():
            items = api("GET", "/api/v1/admin/upnp/devices")["items"]
            timing = next(r for r in items if r["udn"] == d["external_id"])["capabilities"]["airplay"]
            return timing["timing_protocol"] == "NTP" and timing["timing_reason"] == "clock_unavailable"

        until("时钟停止后设备回退至 NTP", fallback)
        peak = until("失钟后新收到第二曲源信号",
                     lambda: recent(at_volume(2, .95 * PEAK, 1.05 * PEAK), first_frames), 20)
        def playing_second():
            state = zone()
            return state["intent"] == "play" and (state.get("current") or {}).get("track", {}).get("track_id") == tracks[1]

        until("失钟后播放区推进到第二曲", playing_second)
        print(f"PTP clock lost: NTP fallback, second-track amplitude={peak:.1f}")
        return
    until("第二曲全音量", lambda: recent(at_volume(2, .95 * PEAK, 1.05 * PEAK), 0), 15)
    check_boundary()
    until("播放区切到第二曲", lambda: (zone().get("current") or {}).get("track", {}).get("track_id") == tracks[1])
    position = zone()["position"]["seconds"]
    until("接收端位置推进", lambda: zone()["position"]["seconds"] > position + .15)
    command("pause")
    start = CAPTURE.stat().st_size // 4
    until("暂停后新收到连续静音 PCM", lambda: recent(lambda data: max(map(abs, data)) <= 3, start, RATE//4))
    paused = zone()
    assert paused["intent"] == "pause", paused["intent"]
    start = CAPTURE.stat().st_size // 4
    command("play")
    restored = until("继续播放时恢复源信号", lambda: recent(at_volume(2, .95 * PEAK, 1.05 * PEAK), start))
    start = CAPTURE.stat().st_size // 4
    volume(.6)
    quieter = until("设备音量衰减", lambda: recent(at_volume(2, 10, PEAK*.8), start))
    print(f"FIFO pause silence >= {RATE//4} frames, resume amplitude={restored:.1f}, volume {peak:.1f} -> {quieter:.1f}")

    before = zone()["revision"]
    rival = subprocess.Popen(["/airplay-probe", "takeover", mode, name])
    try:
        def released():
            state = zone()
            event = state.get("event") or {}
            if (state["intent"] == "pause" and state["status"] == "paused" and state["stream"] is None
                    and event.get("kind") == "pause" and event.get("source") == "device"
                    and event["seq"] > before and device().get("occupied_by") is None):
                return event
            if rival.poll() not in (None, 0):
                raise RuntimeError(f"第二发送端退出 {rival.returncode}")
            return None

        event = until("抢占后设备端暂停并释放会话", released, 24)
        print(f'zones takeover: event={event["kind"]} source={event["source"]} seq={event["seq"]} occupied_by=null')
    finally:
        if rival.poll() is None:
            rival.terminate()
        rival.wait(timeout=5)
    if mode == "airplay2":
        try:
            api("POST", admin_path + "/pair/finish", {"pin": "3939"})
        except RuntimeError as error:
            if 'HTTP 409' not in str(error) or 'pairing_expired' not in str(error):
                raise
            print("admin missing challenge:", error)
        else:
            raise AssertionError("接收端未开始 PIN 配对时意外接受提交")
        assert api("POST", admin_path + "/pair/start", {}, expected=204) is None
        assert api("POST", admin_path + "/pair/finish", {"pin": "3939"}, expected=204) is None
        print("admin PIN start=204 finish=204")
    if clock == "occupied":
        production_controls(tracks)


if __name__ == "__main__":
    if sys.argv[1] == "generate":
        generate(*sys.argv[2:])
    elif sys.argv[1] == "verify-native":
        actual = samples(Path(sys.argv[2]).read_bytes())
        amplitudes = []
        # 每个窗口核对左右声道与原信号的频率、相位关系和幅度，剔除接收端预卷与尾部静音。
        for i in range(0, len(actual) - RATE // 5, RATE // 5):
            match = at_volume(1, PEAK * .98, PEAK * 1.02)(actual[i:i+RATE//5])
            if match:
                amplitudes.append(match)
        matched = len(amplitudes) * RATE // 10
        assert matched >= 3 * RATE, f"原生模式解码出 {matched} 帧匹配的立体声 PCM"
        print(f"native ALSA PCM frames={len(actual)//2} matched={matched} amplitude={max(amplitudes):.1f}")
    else:
        playback(*sys.argv[1:])
