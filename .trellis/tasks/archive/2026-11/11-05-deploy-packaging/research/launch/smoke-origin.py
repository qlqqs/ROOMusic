#!/usr/bin/env python3
"""恰好两次无会话登录探针；密码只在内存生成，不输出或保存请求体。"""
import datetime
import http.client
import json
from pathlib import Path
import secrets
import shlex
import subprocess
import uuid

HERE = Path(__file__).resolve().parent
HOST = '192.168.50.206:8090'
STARTED = datetime.datetime.now(datetime.timezone.utc).isoformat()


def setup_required():
    connection = http.client.HTTPConnection('192.168.50.206', 8090, timeout=10)
    try:
        connection.request('GET', '/api/v1/setup/status')
        response = connection.getresponse()
        body = json.loads(response.read())
        assert response.status == 200 and body['setup_required'] is True
        return True
    finally:
        connection.close()


def main():
    assert setup_required()
    rows = []
    for case, origin, expected_status, expected_code in (
        ('a', 'http://192.168.50.206:8090', 401, 'invalid_credentials'),
        ('b', 'https://music.qlqqs.com:8888', 403, 'origin_forbidden'),
    ):
        # 不输出、不落盘，不通过命令行传密码；HTTPConnection 无自动重试或重定向。
        payload = json.dumps({'username': 'probe', 'password': secrets.token_hex(24)}).encode()
        connection = http.client.HTTPConnection('192.168.50.206', 8090, timeout=10)
        try:
            connection.request('POST', '/api/v1/auth/login', body=payload, headers={
                'Host': HOST, 'Origin': origin, 'Content-Type': 'application/json',
                'X-Request-ID': f'launch-{case}-{uuid.uuid4().hex}',
            })
            response = connection.getresponse()
            body = json.loads(response.read())
            row = dict(case=case, origin=origin, host=HOST, status=response.status,
                       code=body.get('error', {}).get('code'), request_id=body.get('request_id'))
            rows.append(row)
            print(json.dumps(row, ensure_ascii=False), flush=True)
            assert response.status == expected_status and row['code'] == expected_code
            assert row['request_id'] and response.getheader('Set-Cookie') is None
        finally:
            connection.close()
    assert setup_required()
    args = ['docker', 'compose', '-p', 'roomusic-prod', '-f', 'deploy/production/compose.yaml', '--env-file', '.env.prod', 'logs', '--no-color', '--since', STARTED, 'app']
    print('+ ' + shlex.join(args), flush=True)
    # 原始日志只在内存读取；仅记录本次请求的指定字段。
    output = subprocess.check_output(args, text=True)
    ids = {row['request_id'] for row in rows}
    events = []
    fields = {'time', 'level', 'event', 'request_id', 'mode', 'origin', 'host', 'status', 'source', 'method', 'route_template'}
    for line in output.splitlines():
        start = line.find('{')
        if start < 0:
            continue
        try:
            event = json.loads(line[start:])
        except json.JSONDecodeError:
            continue
        if event.get('request_id') in ids:
            selected = {key: value for key, value in event.items() if key in fields}
            events.append(selected)
            print(json.dumps(selected, ensure_ascii=False), flush=True)
    a_id, b_id = rows[0]['request_id'], rows[1]['request_id']
    failed = [event for event in events if event.get('event') == 'identity.login.failed']
    rejected = [event for event in events if event.get('event') == 'identity.origin.rejected']
    assert len(failed) == 1 and failed[0]['request_id'] == a_id
    assert len(rejected) == 1 and rejected[0]['request_id'] == b_id
    assert rejected[0]['mode'] == 'same-host' and rejected[0]['origin'] == rows[1]['origin'] and rejected[0]['host'] == HOST
    for row in rows:
        completed = [event for event in events if event.get('event') == 'http.request.completed' and event['request_id'] == row['request_id']]
        assert len(completed) == 1 and completed[0]['status'] == row['status']
    result = dict(utc=STARTED, login_requests_sent=2, initialization_still_required=True, responses=rows, app_events=events, result='passed')
    (HERE / 'smoke-result.json').write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n')
    print('通过：仅发送两次登录 POST；a=401，b=403 且 mode=same-host；未初始化管理员，没有 Cookie，会话未创建。')


if __name__ == '__main__':
    main()
