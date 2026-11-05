#!/usr/bin/env python3
"""被动读取 app 网络命名空间；仅输出登录请求的四项头部和 TCP 地址，丢弃请求体。"""
import collections
import datetime
import json
import os
import socket
import struct
import sys
import time

ALLOW = {'origin': 'Origin', 'host': 'Host', 'referer': 'Referer', 'user-agent': 'User-Agent'}


class Headers:
    """逐行处理 HTTP/1 请求；非白名单头部和 body 不进入证据或历史缓存。"""
    def __init__(self):
        self.pending = b''
        self.phase = 'request'
        self.remaining = 0
        self.fields = {}
        self.target = False
        self.length = 0
        self.chunked = False

    def feed(self, data):
        records = []
        while data:
            if self.phase == 'ignore':
                return records
            if self.phase == 'body':
                skip = min(self.remaining, len(data))
                self.remaining -= skip
                data = data[skip:]
                if self.remaining == 0:
                    self.phase = 'request'
                continue
            if self.pending:
                data = self.pending + data
                self.pending = b''
            end = data.find(b'\r\n')
            if end < 0:
                if len(self.pending) + len(data) > 16384:
                    self.phase = 'ignore'
                    self.pending = b''
                else:
                    self.pending += data
                return records
            line = self.pending + data[:end]
            self.pending = b''
            data = data[end + 2:]
            if self.phase == 'request':
                self.target = line in (b'POST /api/v1/auth/login HTTP/1.1', b'POST /api/v1/auth/login HTTP/1.0')
                if not line.endswith(b'HTTP/1.1') and not line.endswith(b'HTTP/1.0'):
                    self.phase = 'ignore'
                    return records
                self.fields = {}
                self.length = 0
                self.chunked = False
                self.phase = 'headers'
            elif line:
                key, separator, value = line.partition(b':')
                if not separator:
                    self.phase = 'ignore'
                    return records
                key = key.decode('ascii', 'ignore').lower()
                if self.target and key in ALLOW:
                    # 重复头部保留列表，以便诊断，不将其他头部输出。
                    self.fields.setdefault(ALLOW[key], []).append(value.strip().decode('latin1'))
                elif key == 'content-length':
                    try:
                        self.length = max(0, int(value))
                    except ValueError:
                        self.phase = 'ignore'
                elif key == 'transfer-encoding':
                    self.chunked = True
            else:
                if self.target:
                    records.append({name: self.fields.get(name, []) for name in ALLOW.values()})
                self.fields = {}
                self.target = False
                self.remaining = self.length
                self.phase = 'ignore' if self.chunked else ('body' if self.remaining else 'request')
        return records


def selftest():
    import secrets
    secret = secrets.token_hex(24).encode()
    parser = Headers()
    body = b'{"username":"probe","password":"' + secret + b'"}'
    headers = (b'POST /api/v1/auth/login HTTP/1.1\r\nHost: local.test\r\n'
               b'Origin: https://example.test:8888\r\nReferer: https://example.test:8888/login\r\n'
               b'User-Agent: synthetic-agent\r\nCookie: must-not-be-recorded\r\nContent-Length: '
               + str(len(body)).encode() + b'\r\n\r\n')
    result = []
    # 覆盖分段头部、与 body 同包、持久连接上的后续请求。
    for part in [headers[:37], headers[37:119], headers[119:] + body[:9], body[9:] + headers + body]:
        result.extend(parser.feed(part))
    encoded = json.dumps(result).encode()
    assert len(result) == 2
    assert secret not in encoded and b'must-not-be-recorded' not in encoded
    assert all(set(row) == set(ALLOW.values()) for row in result)
    assert not parser.pending and not parser.fields
    for split in range(1, len(headers)):
        fragmented = Headers()
        rows = fragmented.feed(headers[:split]) + fragmented.feed(headers[split:] + body)
        assert len(rows) == 1 and rows[0] == result[0]
        assert secret not in json.dumps(rows).encode()
    print('合成自检通过：分段头部/持久连接正常，随机 body 与 Cookie 未进入证据或缓存。')


def main():
    # ETH_P_IP；不启用混杂模式，不发送任何报文。
    capture = socket.socket(socket.AF_PACKET, socket.SOCK_RAW, socket.htons(0x0800))
    capture.bind(('eth0', 0))
    capture.settimeout(2)
    # 创建原始套接字后放弃 root 权限；证据文件由本机 uid 1000 创建。
    os.setgid(1000)
    os.setuid(1000)
    os.umask(0o077)
    output = '/evidence/step-08-real-request-headers.jsonl'
    streams = collections.OrderedDict()
    started = time.monotonic()
    count = 0
    def emit(record):
        safe = json.dumps(record, ensure_ascii=True)
        with open(output, 'a', encoding='utf8') as file:
            file.write(safe + '\n')
            file.flush()
            os.fsync(file.fileno())
        print(safe, flush=True)
    emit({'event': 'capture.ready', 'utc': datetime.datetime.now(datetime.timezone.utc).isoformat(),
          'interface': 'eth0', 'destination_tcp_port': 8080,
          'allowed_headers': list(ALLOW.values()), 'max_login_requests': 2,
          'raw_packets_saved': False, 'body_saved': False})
    while count < 2 and time.monotonic() - started < 86400:
        try:
            packet = capture.recv(65535)
        except socket.timeout:
            continue
        if len(packet) < 54 or packet[12:14] != b'\x08\x00':
            continue
        ip = packet[14:]
        ihl = (ip[0] & 15) * 4
        if ip[0] >> 4 != 4 or ip[9] != 6 or len(ip) < ihl + 20:
            continue
        if struct.unpack('!H', ip[6:8])[0] & 0x3fff:
            continue
        tcp = ip[ihl:]
        src_port, dst_port, seq = struct.unpack('!HHI', tcp[:8])
        if dst_port != 8080:
            continue
        offset = (tcp[12] >> 4) * 4
        payload = tcp[offset:]
        source, destination = socket.inet_ntoa(ip[12:16]), socket.inet_ntoa(ip[16:20])
        key = (source, src_port, destination, dst_port)
        flags = tcp[13]
        if flags & 2 or key not in streams:
            streams[key] = [None, Headers()]
        if len(streams) > 128:
            streams.popitem(last=False)
        if not payload:
            if flags & 5:
                streams.pop(key, None)
            continue
        expected, parser = streams[key]
        if expected is not None and seq != expected:
            # 不缓存乱序包或 body；重传已处理部分可裁掉，缺口则安全放弃该流。
            overlap = expected - seq
            if 0 < overlap < len(payload):
                payload = payload[overlap:]
                seq = expected
            elif overlap >= len(payload):
                continue
            else:
                parser.phase = 'ignore'
                parser.pending = b''
                continue
        streams[key][0] = (seq + len(payload)) & 0xffffffff
        for headers in parser.feed(payload):
            count += 1
            emit({'event': 'login.request.headers', 'index': count,
                  'utc': datetime.datetime.now(datetime.timezone.utc).isoformat(),
                  'method': 'POST', 'path': '/api/v1/auth/login',
                  'tcp_peer': f'{source}:{src_port}', 'tcp_server': f'{destination}:{dst_port}',
                  'headers': headers})
            if count >= 2:
                break
    emit({'event': 'capture.finished', 'login_requests': count,
          'utc': datetime.datetime.now(datetime.timezone.utc).isoformat()})


if __name__ == '__main__':
    if '--selftest' in sys.argv:
        selftest()
    else:
        try:
            main()
        except Exception as error:
            # 不输出 traceback 或报文片段，避免异常路径暴露被丢弃数据。
            print(json.dumps({'event': 'capture.failed', 'error_type': type(error).__name__}), flush=True)
            sys.exit(1)
