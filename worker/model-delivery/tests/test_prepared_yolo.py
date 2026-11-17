import ast
import json
from pathlib import Path
import struct
import sys
import tempfile
import unittest
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from common import DeliveryError, file_digest
from native_yolo_validation import validate
from prepare_yolo_workload import prepare


class PreparedYoloTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        records = {}
        for key, name, content in [('model', 'model.rknn', b'explicit test fixture model'),
                                   ('input', 'input.u8', bytes(range(12))),
                                   ('reference', 'reference.npz', b'explicit reference identity fixture')]:
            path = self.root / name
            path.write_bytes(content)
            records[key] = {'file': name, 'sourcePath': '/original/frozen/' + name, 'sha256': file_digest(path)}
        records['input'].update(shape=[2, 2, 3], dtype='uint8', layout='RGB/HWC')
        self.source = {'schemaVersion': 1, 'modelId': 'test-fixture-only', **records}
        (self.root / 'source-manifest.json').write_text(json.dumps(self.source))

    def tearDown(self):
        self.temp.cleanup()

    def test_preparation_only_frames_frozen_bytes_and_bundles_standalone_code(self):
        result = prepare(self.root)
        value = (self.root / 'input.npy').read_bytes()
        self.assertEqual(value[:8], b'\x93NUMPY\x01\x00')
        length = struct.unpack('<H', value[8:10])[0]
        header = ast.literal_eval(value[10:10 + length].decode().strip())
        self.assertEqual(header['shape'], (1, 2, 2, 3))
        self.assertEqual(header['descr'], '|u1')
        self.assertEqual(value[10 + length:], (self.root / 'input.u8').read_bytes())
        workload = json.loads((self.root / 'workload.json').read_text())
        self.assertEqual(workload['provenance']['source'], self.source)
        self.assertEqual(workload['samples'][0]['referenceSha256'], self.source['reference']['sha256'])
        self.assertEqual(result['workloadDigest'], file_digest(self.root / 'workload.json'))
        self.assertEqual({p.name for p in (self.root / 'native').iterdir()},
                         {'native_yolo_validation.py', 'runtime_service.py', 'common.py', 'rk_device_tree.py'})

    def test_changed_frozen_bytes_cannot_generate_new_workload(self):
        (self.root / 'input.u8').write_bytes(b'wrong data')
        with self.assertRaisesRegex(DeliveryError, 'identity'):
            prepare(self.root)
        self.assertFalse((self.root / 'workload.json').exists())

    def test_finite_runner_uses_actual_runtime_evidence_and_always_releases(self):
        prepare(self.root)
        runtime = mock.Mock()
        runtime.infer.return_value = .999
        runtime.observe.return_value = {'modelId': 'test-fixture-only', 'modelHash': self.source['model']['sha256'],
                                        'memoryBytes': 8192, 'observedAt': '2026-11-17T00:00:00Z'}
        factory = mock.Mock(return_value=runtime)
        with mock.patch('native_yolo_validation.sys.platform', 'linux'), \
                mock.patch('native_yolo_validation.platform.machine', return_value='aarch64'):
            report = validate(self.root / 'workload.json', runtime_factory=factory)
        self.assertFalse(report['simulated'])
        self.assertEqual(report['modelHash'], self.source['model']['sha256'])
        self.assertEqual(report['samples'], 1)
        self.assertEqual(runtime.infer.call_count, 1)
        runtime.close.assert_called_once_with()
        runtime.reset_mock()
        runtime.infer.return_value = .2
        with mock.patch('native_yolo_validation.sys.platform', 'linux'), \
                mock.patch('native_yolo_validation.platform.machine', return_value='aarch64'):
            with self.assertRaisesRegex(DeliveryError, 'reference gate'):
                validate(self.root / 'workload.json', runtime_factory=factory)
        runtime.close.assert_called_once_with()

    def test_native_runner_rejects_wrong_machine_before_loading_model(self):
        factory = mock.Mock()
        with mock.patch('native_yolo_validation.platform.machine', return_value='x86_64'):
            with self.assertRaisesRegex(DeliveryError, 'actual ARM64 Linux'):
                validate(self.root / 'workload.json', runtime_factory=factory)
        factory.assert_not_called()

    def test_nonfinite_reference_gate_is_rejected_before_loading_model(self):
        prepare(self.root)
        path = self.root / 'workload.json'
        workload = json.loads(path.read_text())
        workload['minimumQuality'] = float('nan')
        path.write_text(json.dumps(workload))
        factory = mock.Mock()
        with mock.patch('native_yolo_validation.sys.platform', 'linux'), \
                mock.patch('native_yolo_validation.platform.machine', return_value='aarch64'):
            with self.assertRaises(DeliveryError):
                validate(path, runtime_factory=factory)
        factory.assert_not_called()


if __name__ == '__main__':
    unittest.main()
