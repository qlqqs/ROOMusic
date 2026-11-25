"""在测试应用的网卡上读取实际交付的 HTTP 和 FLAC 头。"""

import json
from pathlib import Path
import socket
import struct
import time

HEADER_LIMIT = 8192
FLAC_HEADER_BYTES = 42


def outgoing_http(packet):
    if packet[12:14] != b"\x08\x00" or packet[23] != socket.IPPROTO_TCP:
        return None
    tcp = 14 + (packet[14] & 15) * 4
    source, destination, seq = struct.unpack_from("!HHI", packet, tcp)
    if source != 8080:
        return None
    start = tcp + (packet[tcp + 12] >> 4) * 4
    data = packet[start:]
    if data:
        return (packet[30:34], destination), seq, data
    return None


def stream_info(headers, flac):
    assert headers.startswith(b"HTTP/1.0 200 "), headers
    assert b"\r\ntransfer-encoding:" not in headers.lower(), headers
    assert flac[:8] == b"fLaC\x00\x00\x00\x22" or flac[:8] == b"fLaC\x80\x00\x00\x22", flac[:8]
    packed = int.from_bytes(flac[18:26], "big")
    return {
        "at_ns": time.monotonic_ns(),
        "max_blocksize": int.from_bytes(flac[10:12], "big"),
        "rate": packed >> 44,
        "channels": ((packed >> 41) & 7) + 1,
        "bits": ((packed >> 36) & 31) + 1,
    }


def capture():
    pending = {}
    with socket.socket(socket.AF_PACKET, socket.SOCK_RAW, socket.htons(3)) as tap:
        tap.bind(("eth0", 0))
        with Path("/capture/flac.jsonl").open("w", buffering=1) as report:
            Path("/tmp/flac-probe-ready").touch()
            while True:
                packet, address = tap.recvfrom(65535)
                if address[2] != socket.PACKET_OUTGOING:
                    continue
                payload = outgoing_http(packet)
                if payload is None:
                    continue
                connection, seq, data = payload
                if data.startswith(b"HTTP/"):
                    pending[connection] = seq, bytearray()
                if connection not in pending:
                    continue
                initial_seq, captured = pending[connection]
                offset = (seq - initial_seq) & 0xffffffff
                if offset > len(captured):
                    continue
                captured.extend(data[len(captured) - offset:HEADER_LIMIT - offset])
                boundary = captured.find(b"\r\n\r\n")
                if boundary < 0 and len(captured) < HEADER_LIMIT:
                    continue
                if boundary < 0 or b"\r\ncontent-type: audio/flac" not in captured[:boundary].lower():
                    del pending[connection]
                    continue
                body = boundary + 4
                if len(captured) >= body + FLAC_HEADER_BYTES:
                    row = stream_info(bytes(captured[:boundary]), bytes(captured[body:body + FLAC_HEADER_BYTES]))
                    report.write(json.dumps(row) + "\n")
                    del pending[connection]


if __name__ == "__main__":
    capture()
