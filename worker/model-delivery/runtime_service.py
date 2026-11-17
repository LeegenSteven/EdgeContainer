#!/usr/bin/env python3
"""Image-local RKNN inference validation and source-scoped model observations."""
from __future__ import annotations

import argparse
import asyncio
import hmac
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import inspect
import json
import math
import os
from pathlib import Path
import platform
import signal
import statistics
import subprocess
import sys
import tempfile
import threading
import time
from urllib.request import ProxyHandler, Request, build_opener
import uuid
import wave

from common import DeliveryError, JsonClient, NoRedirect, canonical, digest, file_digest


def now():
    return time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())


def checked_file(root, name, expected):
    path = (Path(root) / name).resolve()
    if not path.is_relative_to(Path(root).resolve()) or file_digest(path) != expected:
        raise DeliveryError('workload_hash_mismatch', 'Frozen validation input does not match its manifest')
    return path


def measured_profile(path):
    manifest = json.loads(Path(path).read_text())
    architecture = {'aarch64': 'arm64', 'x86_64': 'amd64'}.get(platform.machine(), platform.machine())
    if manifest.get('architecture') != architecture or not manifest.get('files'):
        raise DeliveryError('runtime_profile_mismatch', 'Runtime platform differs from its installed profile')
    files, names, paths = [], set(), set()
    for entry in manifest['files']:
        if (not isinstance(entry.get('name'), str) or not entry['name'] or entry['name'] in names
                or not Path(entry['path']).is_absolute() or entry['path'] in paths):
            raise DeliveryError('runtime_profile_invalid', 'Profile files require unique names and absolute paths')
        names.add(entry['name'])
        paths.add(entry['path'])
        actual = file_digest(entry['path'])
        if actual != entry['sha256']:
            raise DeliveryError('runtime_profile_mismatch', 'An installed runtime library or module changed')
        files.append({'name': entry['name'], 'sha256': actual})
    record = {'schemaVersion': 1, 'architecture': architecture, 'backend': manifest['backend'],
              'files': sorted(files, key=lambda entry: entry['name'])}
    return digest(record)


def require_profile_file(config, path):
    manifest = json.loads(Path(config['profileManifest']).read_text())
    resolved = Path(path).resolve()
    if not any(Path(entry['path']).resolve() == resolved for entry in manifest['files']):
        raise DeliveryError('runtime_profile_invalid', 'Runtime configuration and business executable must be profile-bound')


def peak_resident_bytes(pid):
    try:
        for line in Path('/proc/%d/status' % pid).read_text().splitlines():
            if line.startswith('VmHWM:'):
                return int(line.split()[1]) * 1024
    except (OSError, ValueError):
        pass
    raise DeliveryError('runtime_memory_unavailable', 'Actual runtime peak resident memory could not be read')


def character_quality(actual, expected):
    actual, expected = ''.join(actual.split()), ''.join(expected.split())
    if not expected:
        raise DeliveryError('workload_invalid', 'ASR samples need a frozen reference transcript')
    row = list(range(len(expected) + 1))
    for index, character in enumerate(actual, 1):
        updated = [index]
        for offset, reference in enumerate(expected, 1):
            updated.append(min(updated[-1] + 1, row[offset] + 1,
                               row[offset - 1] + (character != reference)))
        row = updated
    return max(0.0, 1 - row[-1] / len(expected))


class YoloRuntime:
    def __init__(self, config):
        source = config.get('deviceTreeFile') or os.environ.get('RKNN_DEVICE_TREE_FILE')
        self.device_tree_receipt = None
        # Native Linux keeps the SDK's real /proc entry unchanged. Containers
        # must present a physical read-only sysfs mount to the existing adapter.
        # That adapter never replaces SoC detection or device availability checks.
        if source or not Path('/proc/device-tree/compatible').is_file():
            from rk_device_tree import install
            try:
                self.device_tree_receipt = install(source or '/run/edgestack-device-tree/compatible')
            except (OSError, ValueError) as error:
                raise DeliveryError('runtime_device_tree_unverified',
                                    'Mount the real RK3588 device tree read-only before initializing RKNN') from error
        from rknnlite.api import RKNNLite
        self.config, self.native, self.initialized = config, RKNNLite(), False
        self.model_hash, self.inferences, self.last_inference = None, 0, None

    def start(self):
        before = file_digest(self.config['modelPath'])
        if self.native.load_rknn(self.config['modelPath']) != 0 or self.native.init_runtime() != 0:
            raise DeliveryError('runtime_load_failed', 'RKNN did not load and initialize the configured model')
        if before != file_digest(self.config['modelPath']):
            raise DeliveryError('runtime_model_changed', 'Model bytes changed while RKNN loaded them')
        self.model_hash, self.initialized = before, True

    def infer(self, sample, root):
        import numpy as np
        if not self.initialized:
            raise DeliveryError('runtime_not_ready', 'RKNN is not initialized')
        source = checked_file(root, sample['input'], sample['inputSha256'])
        reference = checked_file(root, sample['reference'], sample['referenceSha256'])
        value = np.load(source, allow_pickle=False)
        outputs = self.native.inference(inputs=[value])
        if not isinstance(outputs, (list, tuple)) or not outputs:
            raise DeliveryError('runtime_inference_failed', 'RKNN returned no tensor outputs')
        with np.load(reference, allow_pickle=False) as expected:
            if len(outputs) != len(expected.files):
                raise DeliveryError('runtime_output_mismatch', 'RKNN output count differs from the frozen reference')
            passed, total = 0, 0
            for index, output in enumerate(outputs):
                actual, wanted = np.asarray(output), expected['output_%d' % index]
                if actual.shape != wanted.shape or not np.all(np.isfinite(actual)):
                    raise DeliveryError('runtime_output_mismatch', 'RKNN output shape or values are invalid')
                matches = np.isclose(actual, wanted, rtol=sample.get('rtol', .02), atol=sample.get('atol', .0001))
                passed += int(np.count_nonzero(matches))
                total += actual.size
        if not total:
            raise DeliveryError('runtime_output_mismatch', 'Frozen output tensors are empty')
        self.inferences += 1
        self.last_inference = now()
        return passed / total

    def observe(self):
        if (not self.initialized or not self.inferences
                or file_digest(self.config['modelPath']) != self.model_hash):
            raise DeliveryError('runtime_unconfirmed', 'Current RKNN load and inference are unconfirmed')
        return {'modelId': self.config['modelId'], 'modelHash': self.model_hash,
                'observedAt': self.last_inference, 'memoryBytes': peak_resident_bytes(os.getpid())}

    def close(self):
        self.initialized = False
        self.native.release()


class AsrRuntime:
    def __init__(self, config):
        self.config, self.process = config, None
        self.private = tempfile.TemporaryDirectory(prefix='edgestack-asr-')
        self.token_file = Path(self.private.name) / 'upstream.token'
        self.token_file.write_text(uuid.uuid4().hex + uuid.uuid4().hex)
        self.token_file.chmod(0o600)

    def start(self):
        env = dict(os.environ)
        # An approved public ASR service reuses the separately provisioned transport
        # secret. Isolated validation falls back to a fresh in-container credential.
        if env.get('VOICE_UPSTREAM_TOKEN_FILE'):
            self.token_file = Path(env['VOICE_UPSTREAM_TOKEN_FILE'])
        if not self.token_file.read_text().strip():
            raise DeliveryError('asr_transport_unconfigured', 'The ASR transport credential is empty')
        env['VOICE_UPSTREAM_TOKEN_FILE'] = str(self.token_file)
        env.setdefault('EVENT_OUTBOX', str(Path(self.private.name) / 'events'))
        env.setdefault('EVENT_SOURCE_ID', 'local-asr-validation')
        env.setdefault('EVENT_TASK_ID', 'local-asr-validation')
        env['PYTHONPATH'] = str(Path(__file__).parent) + os.pathsep + env.get('PYTHONPATH', '')
        host = self.config.get('asrHost', '127.0.0.1')
        if host not in ('127.0.0.1', '0.0.0.0', '::'):
            raise DeliveryError('asr_bind_invalid', 'ASR bind address must be fixed in the approved image')
        self.process = subprocess.Popen([sys.executable, '-m', 'uvicorn', 'voice_asgi:app', '--host', host,
                                         '--port', str(self.config.get('asrPort', 8621))], env=env,
                                        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, start_new_session=True)
        deadline = time.monotonic() + self.config.get('startupTimeoutSeconds', 300)
        while True:
            if self.process.poll() is not None:
                raise DeliveryError('asr_startup_failed', 'The approved ASR service exited during startup')
            try:
                info = self.info()
                if info.get('asr', {}).get('confirmed') is True:
                    return
            except Exception:
                pass
            if time.monotonic() >= deadline:
                raise DeliveryError('asr_startup_failed', 'ASR runtime initialization was not confirmed')
            time.sleep(1)

    def info(self):
        if self.process is None or self.process.poll() is not None:
            raise DeliveryError('asr_not_ready', 'ASR service is not running')
        client = build_opener(ProxyHandler({}), NoRedirect())
        request = Request('http://127.0.0.1:%d/model-info' % self.config.get('asrPort', 8621),
                          headers={'Authorization': 'Bearer ' + self.token_file.read_text()})
        with client.open(request, timeout=10) as response:
            return json.loads(response.read(1024 * 1024))

    async def recognize(self, pcm):
        import websockets
        headers = {'Authorization': 'Bearer ' + self.token_file.read_text()}
        parameter = 'additional_headers' if 'additional_headers' in inspect.signature(websockets.connect).parameters else 'extra_headers'
        options = {parameter: headers, 'max_size': 4 * 1024 * 1024, 'open_timeout': 30}
        url = 'ws://127.0.0.1:%d/asr/stream' % self.config.get('asrPort', 8621)
        async with websockets.connect(url, **options) as socket:
            await socket.send(json.dumps({'type': 'config', 'sample_rate': 16000,
                                          'asr_language': self.config.get('language', 'zh'),
                                          'business_test_mode': 'synthetic_roundtrip'}))
            for offset in range(0, len(pcm), 3200):
                await socket.send(pcm[offset:offset + 3200])
            await socket.send(json.dumps({'type': 'asr_eos'}))
            deadline = time.monotonic() + self.config.get('inferenceTimeoutSeconds', 120)
            while True:
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    raise DeliveryError('asr_inference_timeout', 'ASR did not produce a final transcript in time')
                frame = await asyncio.wait_for(socket.recv(), remaining)
                if isinstance(frame, bytes):
                    continue
                result = json.loads(frame)
                if result.get('type') == 'error':
                    raise DeliveryError('asr_inference_failed', 'ASR returned an inference error')
                if (result.get('type') == 'asr_final' or result.get('is_final')) and result.get('text'):
                    return result['text']

    def infer(self, sample, root):
        audio = checked_file(root, sample['audio'], sample['audioSha256'])
        with wave.open(str(audio), 'rb') as stream:
            if (stream.getnchannels(), stream.getsampwidth(), stream.getframerate()) != (1, 2, 16000):
                raise DeliveryError('workload_invalid', 'Frozen ASR input must be mono PCM16 at 16 kHz')
            if stream.getnframes() > 16000 * 30:
                raise DeliveryError('workload_invalid', 'ASR validation clips must be at most 30 seconds')
            pcm = stream.readframes(stream.getnframes())
        text = asyncio.run(self.recognize(pcm))
        # The existing observer verifies original RKNN instances and actual encoder inference.
        self.observe()
        return character_quality(text, sample['referenceText'])

    def observe(self):
        from model_receipt import observation
        receipt = observation(self.info(), self.config['slot'], self.config['modelId'])
        if receipt['modelHash'] != file_digest(self.config['modelPath']):
            raise DeliveryError('runtime_model_mismatch', 'ASR loaded a different encoder artifact')
        return {**receipt, 'memoryBytes': peak_resident_bytes(self.process.pid)}

    def close(self):
        if self.process is not None and self.process.poll() is None:
            os.killpg(self.process.pid, signal.SIGTERM)
            try:
                self.process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                os.killpg(self.process.pid, signal.SIGKILL)
                self.process.wait()
        self.private.cleanup()


class RuntimeService:
    def __init__(self, config, runtime=None, config_path=None):
        self.config = config
        self.profile_digest = measured_profile(config['profileManifest'])
        if config_path:
            require_profile_file(config, config_path)
        if config.get('adapter') not in ('asr', 'yolo'):
            raise DeliveryError('runtime_adapter_unsupported', 'Install an approved ASR or YOLO runtime adapter')
        # Candidate identity is written by the fixed build recipe. Runtime hashes
        # the actual file before loading; it never uses this expected hash as proof.
        candidate = Path(__file__).with_name('candidate.json')
        if runtime is None:
            model = json.loads(candidate.read_text())
            if model.get('slot') != config['slot'] or file_digest(config['modelPath']) != model.get('modelHash'):
                raise DeliveryError('runtime_candidate_mismatch', 'Built candidate does not match the installed model')
            config = dict(config, modelId=model['modelId'])
            self.config = config
        self.workload_path = Path(config['workloadManifest'])
        if config_path:
            require_profile_file(config, self.workload_path)
        self.workload = json.loads(self.workload_path.read_text())
        self.workload_digest = file_digest(self.workload_path)
        if (not 1 <= len(self.workload.get('samples', [])) <= 32
                or type(self.workload.get('minimumQuality')) not in (int, float)
                or not math.isfinite(self.workload['minimumQuality'])
                or not 0 <= self.workload['minimumQuality'] <= 1):
            raise DeliveryError('workload_invalid', 'Validation workloads need one to 32 frozen samples')
        self.runtime = runtime or (AsrRuntime(config) if config['adapter'] == 'asr' else YoloRuntime(config))
        self.lock, self.stopped = threading.RLock(), threading.Event()
        self.ready, self.latest, self.cache = False, None, {}
        self.error = 'runtime_starting'
        self.last_measured, self.business = 0, None
        argv = config.get('businessArgv')
        if argv is not None:
            if (not isinstance(argv, list) or not argv or any(not isinstance(arg, str) or '\x00' in arg for arg in argv)
                    or not Path(argv[0]).is_absolute()
                    or Path(argv[0]).name in ('sh', 'bash', 'zsh', 'dash', 'cmd', 'powershell')):
                raise DeliveryError('business_command_invalid', 'Business service requires a fixed absolute executable argument array')
            require_profile_file(config, argv[0])

    def measure(self):
        profile = measured_profile(self.config['profileManifest'])
        if profile != self.profile_digest:
            raise DeliveryError('runtime_profile_changed', 'Runtime files changed after initialization')
        latencies, qualities, failures = [], [], 0
        for sample in self.workload['samples']:
            started = time.monotonic()
            try:
                value = self.runtime.infer(sample, self.workload_path.parent)
                if type(value) not in (int, float) or not math.isfinite(value) or not 0 <= value <= 1:
                    raise DeliveryError('runtime_output_invalid', 'Inference quality must be a measured finite ratio')
                qualities.append(value)
            except Exception:
                failures += 1
            latencies.append((time.monotonic() - started) * 1000)
        observed = self.runtime.observe()
        error_rate = failures / len(self.workload['samples'])
        quality = sum(qualities) / len(self.workload['samples'])
        if failures or quality < self.workload['minimumQuality']:
            self.ready = False
            raise DeliveryError('runtime_workload_failed', 'Real inference did not pass the frozen workload')
        self.latest = {**observed, 'runtimeProfileDigest': profile, 'latencyMs': statistics.mean(latencies),
                       'errorRate': error_rate, 'quality': quality, 'simulated': False, 'healthy': True}
        self.ready, self.error = True, ''
        self.last_measured = time.monotonic()
        return dict(self.latest)

    def health(self):
        # observe() only reads initialized-state receipts; no RKNN call runs here.
        # Keep this available while the serialized workload is doing real inference.
        if self.stopped.is_set() or not self.ready:
            return {'healthy': False, 'errorCode': self.error}
        try:
            if self.business is not None and self.business.poll() is not None:
                raise DeliveryError('business_service_exited', 'The configured business service exited')
            if time.monotonic() - self.last_measured > 120:
                raise DeliveryError('runtime_evidence_stale', 'Actual inference evidence is stale')
            observed = self.runtime.observe()
            if measured_profile(self.config['profileManifest']) != self.profile_digest:
                raise DeliveryError('runtime_profile_changed', 'Runtime profile changed')
            if observed['modelHash'] != self.latest['modelHash']:
                raise DeliveryError('runtime_model_changed', 'Loaded model changed')
            return {'healthy': True, 'modelId': observed['modelId'], 'modelHash': observed['modelHash'],
                    'runtimeProfileDigest': self.profile_digest, 'observedAt': observed['observedAt']}
        except Exception as error:
            return {'healthy': False, 'errorCode': getattr(error, 'code', 'runtime_evidence_unavailable')}

    def validate(self, request):
        if not isinstance(request, dict) or not isinstance(request.get('nonce'), str) or not 1 <= len(request['nonce']) <= 128:
            raise DeliveryError('validation_request_invalid', 'Validation requires a bounded nonce')
        expected = {'deviceId': os.environ['EDGESTACK_MODEL_OBSERVER_DEVICE_ID'],
                    'taskId': os.environ['EDGESTACK_MODEL_OBSERVER_TASK_ID']}
        if (any(request.get(key) != value for key, value in expected.items())
                or request.get('runtimeProfileDigest') != self.profile_digest
                or request.get('inputs') != {'workloadDigest': self.workload_digest}
                or not isinstance(request.get('image'), str) or '@sha256:' not in request['image']):
            raise DeliveryError('validation_identity_mismatch', 'Validation target or frozen workload does not match')
        fingerprint = digest(request)
        with self.lock:
            if not self.ready:
                raise DeliveryError('runtime_not_ready', 'Startup inference has not passed')
            if not self.health()['healthy']:
                raise DeliveryError('runtime_not_ready', 'Current runtime or business service evidence is unavailable')
            cached = self.cache.get(request['nonce'])
            if cached:
                if cached[0] != fingerprint:
                    raise DeliveryError('validation_nonce_conflict', 'Validation nonce was reused for another request')
                return cached[1]
            proof = self.measure()
            if request.get('modelHash') != proof['modelHash']:
                raise DeliveryError('validation_model_mismatch', 'Actual model identity differs from the requested model')
            # Image is a request binding. Server independently verifies the Docker image by its registered task.
            proof.update(expected, nonce=request['nonce'], image=request['image'])
            if len(self.cache) >= 128:
                self.cache.pop(next(iter(self.cache)))
            self.cache[request['nonce']] = (fingerprint, proof)
            return proof

    def publish(self):
        evidence = self.health()
        if not evidence['healthy']:
            return
        client = JsonClient(os.environ['EDGESTACK_SERVER_URL'],
                            os.environ['EDGESTACK_MODEL_OBSERVER_TOKEN_FILE'],
                            os.environ.get('EDGESTACK_SERVER_CA_FILE'))
        receipt = {'slot': os.environ.get('EDGESTACK_MODEL_OBSERVER_SLOT', self.config['slot']),
                   'modelId': evidence['modelId'], 'modelHash': evidence['modelHash'],
                   'observedAt': evidence['observedAt'], 'state': 'loaded'}
        client.request('/api/edgestack/ingest/status', {
            'sourceId': os.environ['EDGESTACK_MODEL_OBSERVER_SOURCE_ID'],
            'taskId': os.environ['EDGESTACK_MODEL_OBSERVER_TASK_ID'],
            'deviceId': os.environ['EDGESTACK_MODEL_OBSERVER_DEVICE_ID'],
            'status': 'healthy', 'observations': [receipt]})

    def start(self):
        def loop():
            try:
                self.runtime.start()
                if self.config.get('businessArgv'):
                    self.business = subprocess.Popen(self.config['businessArgv'], start_new_session=True,
                                                     stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                with self.lock:
                    self.measure()
            except Exception as error:
                self.error = getattr(error, 'code', 'runtime_startup_failed')
                return
            while not self.stopped.is_set():
                try:
                    if not self.ready or time.monotonic() - self.last_measured >= 60:
                        with self.lock:
                            self.measure()
                    self.publish()
                except Exception as error:
                    self.error = getattr(error, 'code', 'runtime_evidence_unavailable')
                    pass  # No fabricated receipt. Read current evidence again on the next interval.
                self.stopped.wait(20)
        self.thread = threading.Thread(target=loop, name='runtime-observer', daemon=True)
        self.thread.start()

    def close(self):
        self.stopped.set()
        self.ready = False
        if self.business is not None and self.business.poll() is None:
            os.killpg(self.business.pid, signal.SIGTERM)
            try:
                self.business.wait(timeout=10)
            except subprocess.TimeoutExpired:
                os.killpg(self.business.pid, signal.SIGKILL)
                self.business.wait()
        self.runtime.close()


def handler(service):
    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass

        def send(self, status, value):
            body = canonical(value)
            self.send_response(status)
            self.send_header('Content-Type', 'application/json')
            self.send_header('Cache-Control', 'no-store')
            self.send_header('Content-Length', str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def do_GET(self):
            if self.path != '/health':
                return self.send(404, {'errorCode': 'not_found'})
            status = service.health()
            return self.send(200 if status['healthy'] else 503, status)

        def do_POST(self):
            if self.path != '/edgestack/validation':
                return self.send(404, {'errorCode': 'not_found'})
            try:
                with Path(os.environ['EDGESTACK_VALIDATION_TOKEN_FILE']).open('rb') as credential:
                    token = credential.read(4097).strip()
            except (OSError, KeyError):
                return self.send(503, {'errorCode': 'validation_unconfigured'})
            if not token or len(token) > 4096 or b'\n' in token or b'\r' in token:
                return self.send(503, {'errorCode': 'validation_unconfigured'})
            if not hmac.compare_digest(self.headers.get('Authorization', '').encode(), b'Bearer ' + token):
                return self.send(401, {'errorCode': 'unauthorized'})
            try:
                if self.headers.get_content_type() != 'application/json':
                    return self.send(400, {'errorCode': 'invalid_body'})
                length = int(self.headers.get('Content-Length', '0'))
                if not 0 < length <= 65536:
                    return self.send(400, {'errorCode': 'invalid_body'})
                result = service.validate(json.loads(self.rfile.read(length)))
                return self.send(200, result)
            except (DeliveryError, OSError, ValueError, KeyError) as error:
                return self.send(409, {'errorCode': getattr(error, 'code', 'validation_failed')})
    return Handler


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--config', required=True)
    args = parser.parse_args()
    config = json.loads(Path(args.config).read_text())
    if config.get('adapter') not in ('asr', 'yolo'):
        raise DeliveryError('runtime_adapter_unsupported', 'Install an approved ASR or YOLO runtime adapter')
    service = RuntimeService(config, config_path=args.config)
    server = ThreadingHTTPServer((config.get('host', '0.0.0.0'), config.get('port', 18622)), handler(service))
    def stop(*_):
        threading.Thread(target=server.shutdown, daemon=True).start()
    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    service.start()
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()
        service.close()


if __name__ == '__main__':
    main()
