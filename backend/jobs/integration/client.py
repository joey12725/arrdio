#!/usr/bin/env python3
"""Portable ARRDIO CLI and stdio-to-HTTP MCP adapter. Python 3.10+, stdlib only."""
import argparse
import base64
import json
import os
from pathlib import Path
import sys
import urllib.error
import urllib.request

CONFIG = Path(os.environ.get('ARRDIO_CONFIG', '~/.config/arrdio/connection.json')).expanduser()

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None

def connection():
    saved = json.loads(CONFIG.read_text()) if CONFIG.exists() else {}
    return os.environ.get('ARRDIO_URL', saved.get('resource', '')).rstrip('/'), os.environ.get('ARRDIO_TOKEN', saved.get('token', ''))

def request(method, path, body=None, key=None, auth=True):
    base, token = connection()
    if not base or not path.startswith('/') or path.startswith('//'):
        raise ValueError('Configure ARRDIO_URL and a relative API path')
    headers = {'Content-Type': 'application/json', 'Accept': 'application/json, text/event-stream'}
    if auth:
        headers.update({'Authorization': 'Bearer ' + token, 'X-Arrdio-Resource': base})
    if key:
        headers['Idempotency-Key'] = key
    if path == '/mcp' and isinstance(body, dict):
        meta = body.get('params', {}).get('_meta', {})
        version = meta.get('io.modelcontextprotocol/protocolVersion')
        if version:
            headers['MCP-Protocol-Version'] = version
            headers['Mcp-Method'] = body.get('method', '')
            name = body.get('params', {}).get('name') or body.get('params', {}).get('uri')
            if name:
                if not name.isascii() or name.strip() != name or any(ord(c) < 32 for c in name) or (name.startswith('=?base64?') and name.endswith('?=')):
                    name = '=?base64?' + base64.b64encode(name.encode()).decode() + '?='
                headers['Mcp-Name'] = name
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(base + path, data=data, headers=headers, method=method)
    try:
        response = urllib.request.build_opener(NoRedirect()).open(req, timeout=120)
    except urllib.error.HTTPError as error:
        if path == '/mcp':
            payload = json.loads(error.read())
            if payload.get('jsonrpc') == '2.0' and 'error' in payload:
                return payload
        raise
    with response:
        raw = response.read()
        return json.loads(raw) if raw else None

def pair():
    base, _ = connection()
    result = request('POST', '/pair', {'bootstrap': os.environ['ARRDIO_BOOTSTRAP'], 'resource': base, 'label': 'portable-agent'}, auth=False)
    CONFIG.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    temp = CONFIG.with_suffix('.tmp')
    fd = os.open(temp, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    try:
        with os.fdopen(fd, 'w') as output:
            json.dump(result, output)
            output.flush()
            os.fsync(output.fileno())
        os.replace(temp, CONFIG)
    finally:
        temp.unlink(missing_ok=True)
    print(json.dumps({'paired': True, 'owner': result['owner'], 'config': str(CONFIG)}))

def mcp():
    for line in sys.stdin:
        message = None
        try:
            message = json.loads(line)
            response = request('POST', '/mcp', message)
            if response is not None:
                print(json.dumps(response), flush=True)
        except Exception:
            if isinstance(message, dict) and 'id' in message:
                print(json.dumps({'jsonrpc': '2.0', 'id': message['id'], 'error': {'code': -32603, 'message': 'ARRDIO request failed; check connection and grant'}}), flush=True)

def main():
    p = argparse.ArgumentParser()
    p.add_argument('command', choices=['pair', 'me', 'status', 'quota', 'requests', 'enqueue', 'request', 'mcp'])
    p.add_argument('args', nargs='*')
    p.add_argument('--key')
    a = p.parse_args()
    if a.command == 'pair':
        pair(); return
    if a.command == 'mcp':
        mcp(); return
    if a.command == 'enqueue':
        if not a.key:
            p.error('--key is required')
        result = request('POST', '/requests', json.load(sys.stdin), a.key)
    elif a.command == 'request':
        method, path = a.args
        result = request(method, path, json.load(sys.stdin) if method in ('POST', 'PUT', 'PATCH') and not sys.stdin.isatty() else None, a.key)
    else:
        result = request('GET', '/' + a.command)
    print(json.dumps(result, indent=2))

if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        print('ARRDIO operation failed (' + type(error).__name__ + '); no credentials logged', file=sys.stderr)
        sys.exit(1)
