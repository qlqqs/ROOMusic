#!/usr/bin/env python3
"""拥有本轮隔离服务进程；通过临时 Unix socket 重启，不按端口或进程名杀服务。"""

import os
import signal
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.request


def restart(address):
    with socket.socket(socket.AF_UNIX) as client:
        client.settimeout(40)
        client.connect(address)
        client.sendall(b"restart\n")
        reply = client.recv(4096).decode()
        if reply != "ready\n":
            raise RuntimeError(reply)


def serve(binary, log_path, address):
    process = None

    def stop():
        nonlocal process
        if process is not None:
            process.terminate()
            try:
                process.wait(timeout=15)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()
                raise RuntimeError("隔离服务未能正常退出")
            process = None

    def terminate(_signal, _frame):
        raise SystemExit(0)

    signal.signal(signal.SIGTERM, terminate)
    signal.signal(signal.SIGINT, terminate)
    try:
        with open(log_path, "ab", buffering=0) as log, socket.socket(socket.AF_UNIX) as control:
            control.bind(address)
            control.listen(1)
            process = subprocess.Popen([binary], stdout=log, stderr=log)
            while True:
                connection, _ = control.accept()
                with connection:
                    if connection.recv(64) != b"restart\n":
                        connection.sendall(b"unknown command\n")
                        continue
                    try:
                        stop()
                        process = subprocess.Popen([binary], stdout=log, stderr=log)
                        deadline = time.monotonic() + 20
                        while True:
                            if process.poll() is not None:
                                raise RuntimeError("重启后的隔离服务提前退出")
                            try:
                                with urllib.request.urlopen(
                                    os.environ["ROOMUSIC_PLAYBACK_BASE_URL"] + "/api/v1/setup/status", timeout=1
                                ) as response:
                                    if response.status == 200:
                                        break
                            except (urllib.error.URLError, TimeoutError):
                                pass
                            if time.monotonic() >= deadline:
                                raise RuntimeError("重启后的隔离服务未就绪")
                            time.sleep(0.1)  # 仅轮询 HTTP 就绪，不代替播放状态等待。
                        connection.sendall(b"ready\n")
                    except Exception as error:
                        connection.sendall((str(error) + "\n").encode())
                        raise
    finally:
        # 清理期间不再被外层 TERM 或终端 INT 打断，确保子进程被回收。
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
        signal.signal(signal.SIGINT, signal.SIG_IGN)
        stop()


if __name__ == "__main__":
    if sys.argv[1] == "--restart":
        restart(sys.argv[2])
    else:
        serve(*sys.argv[1:])
