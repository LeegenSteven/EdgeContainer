"""Small authenticated transports and durable execution records for delivery workers."""
from __future__ import annotations

import hashlib
import json
import os
import re
from pathlib import Path
import signal
import sqlite3
import ssl
import subprocess
import tempfile
import threading
import time
from urllib.error import HTTPError, URLError
from urllib.parse import urlsplit
from urllib.request import HTTPRedirectHandler, HTTPSHandler, Request, build_opener


class DeliveryError(Exception):
    def __init__(self, code, message):
        self.code = code
        super().__init__(message)


class TransportError(DeliveryError):
    """A request may have reached its destination. Mutations need read-back."""


class ApiError(DeliveryError):
    def __init__(self, status, body):
        self.status = status
        self.body = body
        super().__init__(body.get('errorCode') or body.get('code') or 'http_error',
                         'API returned HTTP %d' % status)


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(',', ':'), ensure_ascii=False,
                      allow_nan=False).encode('utf-8')


def digest(value):
    return 'sha256:' + hashlib.sha256(canonical(value)).hexdigest()


def file_digest(path):
    hasher = hashlib.sha256()
    with Path(path).open('rb') as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b''):
            hasher.update(block)
    return 'sha256:' + hasher.hexdigest()


def atomic_bytes(path, data):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor, temporary = tempfile.mkstemp(dir=str(path.parent), prefix='.delivery-')
    try:
        with os.fdopen(descriptor, 'wb') as stream:
            stream.write(data)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
        directory = os.open(str(path.parent), os.O_RDONLY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def opener(ca_file=None):
    context = ssl.create_default_context(cafile=ca_file)
    return build_opener(NoRedirect(), HTTPSHandler(context=context))


class JsonClient:
    """A configured origin and fixed adapter paths. Tokens never follow redirects."""
    def __init__(self, base_url, token_file=None, ca_file=None, timeout=30):
        parsed = urlsplit(base_url)
        if (parsed.scheme not in ('http', 'https') or not parsed.hostname or parsed.username
                or parsed.password or parsed.query or parsed.fragment):
            raise DeliveryError('invalid_service_url', 'Invalid configured service URL')
        self.base_url = base_url.rstrip('/')
        self.token_file = Path(token_file) if token_file else None
        self.timeout = timeout
        self.opener = opener(ca_file)

    def request(self, path, data=None, method=None):
        attempts = 3 if data is None and method in (None, 'GET') else 1
        for attempt in range(attempts):
            try:
                return self._request_once(path, data, method)
            except TransportError:
                if attempt + 1 == attempts:
                    raise
                time.sleep(1)

    def _request_once(self, path, data=None, method=None):
        if not path.startswith('/') or path.startswith('//') or '://' in path:
            raise DeliveryError('invalid_api_path', 'Adapter paths must be relative to their configured origin')
        headers = {'Accept': 'application/json'}
        if self.token_file:
            token = self.token_file.read_text().strip()
            if not token or '\r' in token or '\n' in token:
                raise DeliveryError('invalid_token', 'The configured token file is empty or malformed')
            headers['Authorization'] = 'Bearer ' + token
        body = None
        if data is not None:
            headers['Content-Type'] = 'application/json'
            body = canonical(data)
        request = Request(self.base_url + path, data=body, headers=headers,
                          method=method or ('POST' if data is not None else 'GET'))
        try:
            with self.opener.open(request, timeout=self.timeout) as response:
                raw = response.read(4 * 1024 * 1024 + 1)
                if len(raw) > 4 * 1024 * 1024:
                    raise DeliveryError('response_too_large', 'API response exceeded the adapter limit')
                value = json.loads(raw)
                if not isinstance(value, dict):
                    raise ValueError('not an object')
                return value
        except HTTPError as error:
            raw = error.read(65536)
            try:
                value = json.loads(raw)
                if not isinstance(value, dict):
                    value = {}
            except (ValueError, OSError):
                # Some narrow Go handlers use http.Error. Preserve only a bounded
                # machine code, never arbitrary response text or credentials.
                code = raw.decode('ascii', errors='ignore').strip()
                value = {'errorCode': code} if re.fullmatch(r'[a-z][a-z0-9_]{1,100}', code) else {}
            raise ApiError(error.code, value) from None
        except (URLError, OSError, ValueError) as error:
            raise TransportError('transport_unknown', 'Service response was not confirmed') from error


class Journal:
    """Every external mutation gets a durable intent before it is sent."""
    def __init__(self, path):
        path = Path(path)
        path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        self.connection = sqlite3.connect(str(path))
        os.chmod(path, 0o600)
        self.connection.execute('PRAGMA journal_mode=WAL')
        self.connection.execute('PRAGMA synchronous=FULL')
        self.connection.execute('CREATE TABLE IF NOT EXISTS jobs (id TEXT PRIMARY KEY, body TEXT NOT NULL)')

    def get(self, job_id):
        row = self.connection.execute('SELECT body FROM jobs WHERE id=?', (job_id,)).fetchone()
        return json.loads(row[0]) if row else {}

    def save(self, job_id, body):
        with self.connection:
            self.connection.execute('INSERT OR REPLACE INTO jobs VALUES (?,?)',
                                    (job_id, canonical(body).decode()))

    def bind(self, job):
        identity = {key: job.get(key) for key in ('id', 'kind', 'recipeId', 'recipe', 'recipeDigest', 'source',
                                                'deviceId', 'validationDeviceId', 'validationTaskId',
                                                'policyId', 'policyVersion')}
        fingerprint = digest(identity)
        state = self.get(job['id'])
        if state and state.get('fingerprint') != fingerprint:
            raise DeliveryError('job_identity_changed', 'Job inputs changed after an external operation')
        if not state:
            state = {'fingerprint': fingerprint}
            self.save(job['id'], state)
        return state

    def close(self):
        self.connection.close()


class Commands:
    """Run fixed argument arrays, bounded by a timeout and the claim lease."""
    def __init__(self, guard=None):
        self.guard = guard

    def run(self, argv, timeout=600, cwd=None):
        if not isinstance(argv, list) or not argv or not all(isinstance(v, str) for v in argv):
            raise DeliveryError('invalid_command', 'Expected a fixed argument array')
        if self.guard:
            self.guard.check()
        with tempfile.TemporaryFile() as stdout, tempfile.TemporaryFile() as stderr:
            try:
                process = subprocess.Popen(argv, cwd=cwd, stdin=subprocess.DEVNULL,
                                           stdout=stdout, stderr=stderr, start_new_session=True)
            except FileNotFoundError as error:
                raise DeliveryError('backend_unavailable', 'A configured execution backend is unavailable') from error
            deadline = time.monotonic() + timeout
            try:
                while process.poll() is None:
                    if self.guard:
                        self.guard.check()
                    if time.monotonic() >= deadline:
                        raise DeliveryError('command_timeout', 'The configured backend timed out')
                    time.sleep(.1)
            except BaseException:
                try:
                    os.killpg(process.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                process.wait()
                raise
            if process.returncode:
                # Commands may print registry credentials or signed URLs. Keep their output local.
                raise DeliveryError('backend_failed', 'Execution backend exited with code %d' % process.returncode)
            stdout.seek(0)
            result = stdout.read(4 * 1024 * 1024 + 1)
            if len(result) > 4 * 1024 * 1024:
                raise DeliveryError('backend_response_too_large', 'Backend output exceeded the adapter limit')
            return result
