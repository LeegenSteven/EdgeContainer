"""Protocol and evidence regressions with explicit fake hardware, never a deployment."""
import json
import os
from pathlib import Path
import platform
import sys
import tempfile
import threading
import time
import types
import unittest
from unittest import mock
from urllib.error import HTTPError
from urllib.request import ProxyHandler, Request, build_opener
from http.server import ThreadingHTTPServer

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
sys.path.insert(0, str(Path(__file__).resolve().parents[2] / 'business-events'))
from common import DeliveryError, canonical, digest, file_digest
from runtime_service import (AsrRuntime, RuntimeService, YoloRuntime, character_quality,
                             checked_file, handler, measured_profile)


class HardwareFixture:
    """Fake hardware is injected only by these tests."""
    def __init__(self, model):
        self.model, self.calls, self.started = model, 0, False
        self.fail = False

    def start(self):
        self.started = True

    def infer(self, sample, root):
        self.calls += 1
        if self.fail:
            raise DeliveryError('hardware_failed', 'fixture inference failed')
        return .98

    def observe(self):
        if not self.started or not self.calls:
            raise DeliveryError('runtime_unconfirmed', 'fixture has no inference')
        return {'modelId': 'candidate', 'modelHash': file_digest(self.model),
                'observedAt': time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime()), 'memoryBytes': 4096}

    def close(self):
        self.started = False


class RuntimeTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.library = self.root / 'native.so'
        self.library.write_bytes(b'installed library fixture')
        self.model = self.root / 'candidate.rknn'
        self.model.write_bytes(b'candidate fixture')
        self.workload = self.root / 'workload.json'
        self.workload.write_bytes(canonical({'minimumQuality': .9, 'samples': [{'sample': 'frozen'}]}))
        self.profile = self.root / 'profile.json'
        self.architecture = {'aarch64': 'arm64', 'x86_64': 'amd64'}.get(platform.machine(), platform.machine())
        self.profile.write_bytes(canonical({'schemaVersion': 1, 'architecture': self.architecture, 'backend': 'rknn-yolo',
                                           'files': [{'name': 'native-runtime', 'path': str(self.library),
                                                      'sha256': file_digest(self.library)}]}))
        self.config = {'adapter': 'yolo', 'modelPath': str(self.model), 'slot': 'vision',
                       'profileManifest': str(self.profile), 'workloadManifest': str(self.workload)}
        self.hardware = HardwareFixture(self.model)
        self.service = RuntimeService(self.config, runtime=self.hardware)
        self.env = mock.patch.dict(os.environ, {'EDGESTACK_MODEL_OBSERVER_TASK_ID': 'test-task',
                                               'EDGESTACK_MODEL_OBSERVER_DEVICE_ID': 'test-device'})
        self.env.start()

    def tearDown(self):
        self.env.stop()
        self.temp.cleanup()

    def ready(self):
        self.hardware.start()
        self.service.measure()

    def request(self):
        return {'deviceId': 'test-device', 'taskId': 'test-task', 'nonce': 'stable-nonce',
                'image': 'registry.test/models@sha256:' + 'a' * 64,
                'modelHash': file_digest(self.model), 'runtimeProfileDigest': self.service.profile_digest,
                'inputs': {'workloadDigest': file_digest(self.workload)}}

    def test_health_requires_actual_inference_and_rejects_changed_profile_model_and_stale_evidence(self):
        self.assertFalse(self.service.health()['healthy'])
        self.hardware.start()
        self.assertFalse(self.service.health()['healthy'])
        self.service.measure()
        self.assertTrue(self.service.health()['healthy'])
        self.assertGreater(self.hardware.calls, 0)
        self.library.write_bytes(b'changed native runtime')
        self.assertFalse(self.service.health()['healthy'])
        self.library.write_bytes(b'installed library fixture')
        self.model.write_bytes(b'changed model')
        self.assertEqual(self.service.health()['errorCode'], 'runtime_model_changed')
        self.model.write_bytes(b'candidate fixture')
        self.service.last_measured = time.monotonic() - 121
        self.assertEqual(self.service.health()['errorCode'], 'runtime_evidence_stale')

    def test_failed_real_workload_cannot_make_health_ready(self):
        self.hardware.start()
        self.hardware.fail = True
        with self.assertRaisesRegex(DeliveryError, 'Real inference'):
            self.service.measure()
        self.assertFalse(self.service.health()['healthy'])
        self.hardware.fail = False
        self.hardware.infer = lambda *_: float('nan')
        with self.assertRaises(DeliveryError):
            self.service.measure()
        self.assertFalse(self.service.health()['healthy'])

    def test_nonce_is_idempotent_and_identity_and_workload_are_bound(self):
        self.ready()
        request = self.request()
        proof = self.service.validate(request)
        self.assertEqual(proof['modelHash'], file_digest(self.model))
        self.assertFalse(proof['simulated'])
        calls = self.hardware.calls
        self.assertEqual(self.service.validate(request), proof)
        self.assertEqual(self.hardware.calls, calls)
        with self.assertRaisesRegex(DeliveryError, 'nonce'):
            self.service.validate(dict(request, image='other@sha256:' + 'b' * 64))
        with self.assertRaisesRegex(DeliveryError, 'workload'):
            self.service.validate(dict(request, inputs={'workloadDigest': 'unapproved'}))
        with self.assertRaisesRegex(DeliveryError, 'Actual model'):
            self.service.validate(dict(request, nonce='new-nonce', modelHash='sha256:' + '0' * 64))
        with self.assertRaises(DeliveryError):
            self.service.validate([])

    def test_profile_measures_actual_libraries_and_platform_and_requires_unique_files(self):
        expected = digest({'schemaVersion': 1, 'architecture': self.architecture, 'backend': 'rknn-yolo',
                           'files': [{'name': 'native-runtime', 'sha256': file_digest(self.library)}]})
        self.assertEqual(measured_profile(self.profile), expected)
        record = json.loads(self.profile.read_text())
        record['files'] *= 2
        self.profile.write_bytes(canonical(record))
        with self.assertRaisesRegex(DeliveryError, 'unique'):
            measured_profile(self.profile)
        record['files'] = record['files'][:1]
        record['architecture'] = 'uninstalled-platform'
        self.profile.write_bytes(canonical(record))
        with self.assertRaisesRegex(DeliveryError, 'platform'):
            measured_profile(self.profile)

    def test_input_paths_hashes_and_asr_reference_quality(self):
        clip = self.root / 'reference.wav'
        clip.write_bytes(b'frozen')
        self.assertEqual(checked_file(self.root, clip.name, file_digest(clip)), clip.resolve())
        with self.assertRaises(DeliveryError):
            checked_file(self.root / 'child', '../reference.wav', file_digest(clip))
        self.assertEqual(character_quality('你 好', '你好'), 1)
        self.assertEqual(character_quality('你', '你好'), .5)
        with self.assertRaises(DeliveryError):
            character_quality('anything', '')

    def test_business_service_exit_suppresses_health_and_observation(self):
        self.ready()
        self.service.business = mock.Mock()
        self.service.business.poll.return_value = 7
        self.assertEqual(self.service.health()['errorCode'], 'business_service_exited')
        with mock.patch('runtime_service.JsonClient') as client:
            self.service.publish()
            client.assert_not_called()

    def test_observer_source_uses_current_loaded_hash_not_desired_environment(self):
        self.ready()
        with mock.patch.dict(os.environ, {'EDGESTACK_MODEL_OBSERVER_SOURCE_ID': 'private-observer',
                                         'EDGESTACK_MODEL_OBSERVER_TOKEN_FILE': '/private/token',
                                         'EDGESTACK_SERVER_URL': 'https://server.test',
                                         'MODEL_SHA256': 'invented-hash'}):
            with mock.patch('runtime_service.JsonClient') as client:
                self.service.publish()
        path, payload = client.return_value.request.call_args.args
        self.assertEqual(path, '/api/edgestack/ingest/status')
        self.assertEqual(payload['sourceId'], 'private-observer')
        self.assertEqual(payload['observations'][0]['modelHash'], file_digest(self.model))
        self.assertEqual(payload['observations'][0]['state'], 'loaded')

    def test_http_validation_requires_independent_secret_and_handles_non_object_json(self):
        self.ready()
        server = ThreadingHTTPServer(('127.0.0.1', 0), handler(self.service))
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        client = build_opener(ProxyHandler({}))
        url = 'http://127.0.0.1:%d/edgestack/validation' % server.server_port
        token_file = self.root / 'validation.token'
        token_file.write_text('independent-secret')
        def post(body, token):
            request = Request(url, canonical(body), {'Content-Type': 'application/json', 'Authorization': 'Bearer ' + token})
            return client.open(request, timeout=2)
        try:
            with mock.patch.dict(os.environ, {'EDGESTACK_VALIDATION_TOKEN_FILE': str(token_file)}):
                with self.assertRaises(HTTPError) as denied:
                    post(self.request(), 'observer-secret')
                self.assertEqual(denied.exception.code, 401)
                with self.assertRaises(HTTPError) as invalid:
                    post([], 'independent-secret')
                self.assertEqual(invalid.exception.code, 409)
                with post(self.request(), 'independent-secret') as response:
                    self.assertTrue(json.load(response)['healthy'])
            token_file.unlink()
            with mock.patch.dict(os.environ, {'EDGESTACK_VALIDATION_TOKEN_FILE': str(token_file)}):
                with self.assertRaises(HTTPError) as absent:
                    post(self.request(), 'independent-secret')
                self.assertEqual(absent.exception.code, 503)
        finally:
            server.shutdown()
            server.server_close()
            thread.join(2)

    def test_rknn_load_or_init_failure_cannot_create_loaded_receipt(self):
        native = mock.Mock()
        module = types.ModuleType('rknnlite.api')
        module.RKNNLite = lambda: native
        with mock.patch.dict(sys.modules, {'rknnlite': types.ModuleType('rknnlite'), 'rknnlite.api': module}), \
                mock.patch('rk_device_tree.install', return_value={'path': '/physical-fixture', 'sha256': 'fixture'}) as install:
            runtime = YoloRuntime({'modelPath': str(self.model), 'modelId': 'candidate',
                                   'deviceTreeFile': '/run/edgestack-device-tree/compatible'})
            install.assert_called_once_with('/run/edgestack-device-tree/compatible')
            native.load_rknn.return_value = 1
            with self.assertRaises(DeliveryError):
                runtime.start()
            with self.assertRaises(DeliveryError):
                runtime.observe()
            native.load_rknn.return_value, native.init_runtime.return_value = 0, 1
            with self.assertRaises(DeliveryError):
                runtime.start()
            self.assertFalse(runtime.initialized)

    def test_yolo_copied_identity_file_is_rejected_before_constructing_rknn(self):
        # Matching bytes alone never stand in for a read-only physical sysfs mount.
        copied = self.root / 'copied-compatible'
        copied.write_bytes(b'rockchip,aibox-pro-3588jd4\x00rockchip,rk3588\x00')
        module = types.ModuleType('rknnlite.api')
        module.RKNNLite = mock.Mock()
        with mock.patch.dict(sys.modules, {'rknnlite': types.ModuleType('rknnlite'), 'rknnlite.api': module}):
            with self.assertRaisesRegex(DeliveryError, 'real RK3588 device tree'):
                YoloRuntime({'modelPath': str(self.model), 'modelId': 'candidate', 'deviceTreeFile': str(copied)})
        module.RKNNLite.assert_not_called()

    def test_yolo_native_proc_entry_preserves_original_sdk_detection(self):
        module = types.ModuleType('rknnlite.api')
        module.RKNNLite = mock.Mock()
        with mock.patch.dict(sys.modules, {'rknnlite': types.ModuleType('rknnlite'), 'rknnlite.api': module}), \
                mock.patch.dict(os.environ, {'RKNN_DEVICE_TREE_FILE': ''}), \
                mock.patch('runtime_service.Path.is_file', return_value=True), \
                mock.patch('rk_device_tree.install') as install:
            YoloRuntime({'modelPath': str(self.model), 'modelId': 'candidate'})
        install.assert_not_called()
        module.RKNNLite.assert_called_once_with()

    def test_asr_reuses_approved_transport_secret_and_bind_without_launching_a_real_service(self):
        token = self.root / 'existing-transport.token'
        token.write_text('approved-existing-transport')
        runtime = AsrRuntime({'asrHost': '0.0.0.0', 'asrPort': 8621, 'modelPath': str(self.model)})
        process = mock.Mock()
        process.poll.return_value = None
        with mock.patch.dict(os.environ, {'VOICE_UPSTREAM_TOKEN_FILE': str(token)}), \
                mock.patch('runtime_service.subprocess.Popen', return_value=process) as launch, \
                mock.patch.object(runtime, 'info', return_value={'asr': {'confirmed': True}}):
            runtime.start()
        argv = launch.call_args.args[0]
        self.assertEqual(argv[argv.index('--host') + 1], '0.0.0.0')
        self.assertEqual(argv[argv.index('--port') + 1], '8621')
        self.assertEqual(launch.call_args.kwargs['env']['VOICE_UPSTREAM_TOKEN_FILE'], str(token))
        runtime.process = None
        runtime.close()


if __name__ == '__main__':
    unittest.main()
