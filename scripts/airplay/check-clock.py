"""在本次隔离实例内对照发送端时钟和接收端 nqptp 读数。"""
import json
import struct
import subprocess
import sys
import time


def inside(service, *args):
    return subprocess.check_output(compose + ["exec", "-T", service, *args])


def status(peer=None):
    query = "?peer=" + peer if peer else ""
    return json.loads(inside("app", "wget", "-qO-", "http://127.0.0.1:55440/status" + query))


def check_receiver():
    peer = inside("receiver", "hostname", "-i").decode().split()[0]
    # 直接在接收端采样，容器命令的启动延迟不会跳过短暂的时钟变化。
    sample = """
      i=0
      while [ "$i" -lt 600 ]; do
        if [ -s /dev/shm/nqptp ]; then
          read -r now idle < /proc/uptime
          printf '%s ' "$now"
          od -An -v -tx1 /dev/shm/nqptp | tr -d ' \n'
          echo
        fi
        sleep .1
        i=$((i + 1))
      done
    """
    command = compose + ["exec", "-T", "receiver", "sh", "-c", sample]
    with subprocess.Popen(command, stdout=subprocess.PIPE, text=True) as reader:
        seen = None
        stable_since = None
        last_sample = 0
        last = {}
        try:
            for line in reader.stdout:
                elapsed, hex_data = line.split()
                now = float(elapsed)
                data = bytes.fromhex(hex_data)
                if len(data) != 88 or int.from_bytes(data[:2], sys.byteorder) != 11:
                    raise ValueError(f"nqptp 共享区大小或版本不符：{len(data)}, {data[:2].hex()}")
                main, secondary = struct.unpack_from("=Q32xQ", data, 8)
                if (main, secondary) != seen or now - last_sample >= .4:
                    last = status(peer)
                    last_sample = now
                if (main, secondary) != seen:
                    seen = (main, secondary)
                    stable_since = None
                    print(f"nqptp v11 bytes={len(data)} main={main:016x} secondary={secondary:016x}",
                          "peer_status=", last, flush=True)
                valid = main and main == secondary and data[8:48] == data[48:88]
                if valid and last["available"] and last["grandmaster_id"] == main:
                    if stable_since is None:
                        stable_since = now
                    if now - stable_since >= 2:
                        print(f"PTP selected: role={last['role']} sender_grandmaster={main:016x} receiver_nqptp={secondary:016x}",
                              f"stable={now - stable_since:.2f}s", flush=True)
                        return
                else:
                    stable_since = None
        finally:
            reader.terminate()
    raise SystemExit(f"接收端未选中发送端的主时钟：peer={status(peer)}, local={status()}, nqptp={seen}")


mode, *compose = sys.argv[1:]
if mode == "ptp":
    check_receiver()
else:
    last = {}
    deadline = time.monotonic() + 12
    while time.monotonic() < deadline:
        try:
            last = status()
        except subprocess.CalledProcessError:
            time.sleep(.25)
            continue
        if last["available"] is False and last["reason"] == "port_in_use":
            print("PTP port collision:", last["reason"])
            break
        time.sleep(.25)
    else:
        raise SystemExit(f"未检出端口占用：{last}")
