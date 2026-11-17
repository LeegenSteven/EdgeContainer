#!/usr/bin/env python3
"""Seal already-frozen uint8 YOLO inputs into a source-independent local package."""
import argparse
import json
import math
from pathlib import Path
import shutil
import struct

from common import DeliveryError, atomic_bytes, canonical, file_digest


def prepare(directory):
    root = Path(directory).resolve()
    source = json.loads((root / 'source-manifest.json').read_text())
    for key in ('model', 'input', 'reference'):
        record = source[key]
        if Path(record['file']).name != record['file']:
            raise DeliveryError('workload_path_invalid', 'Frozen asset filenames must stay in the package directory')
        path = root / record['file']
        if path.is_symlink() or file_digest(path) != record['sha256']:
            raise DeliveryError('workload_hash_mismatch', 'An existing frozen asset differs from its recorded identity')
    shape = source['input']['shape']
    if (not isinstance(shape, list) or len(shape) != 3 or shape[-1] != 3
            or any(type(value) is not int or value <= 0 for value in shape)
            or math.prod(shape) > 64 * 1024 * 1024
            or source['input'].get('dtype') != 'uint8' or source['input'].get('layout') != 'RGB/HWC'):
        raise DeliveryError('workload_shape_invalid', 'Use the frozen RGB HWC uint8 input contract')
    value = (root / source['input']['file']).read_bytes()
    if len(value) != math.prod(shape):
        raise DeliveryError('workload_shape_invalid', 'Frozen input byte count differs from its declared shape')
    # NPY v1.0 framing only: preserve every input byte and add one batch dimension.
    # No image decode, resizing, color conversion or new reference inference occurs.
    header = repr({'descr': '|u1', 'fortran_order': False, 'shape': tuple([1] + shape)}).encode('ascii')
    header += b' ' * (-(10 + len(header) + 1) % 64) + b'\n'
    atomic_bytes(root / 'input.npy', b'\x93NUMPY\x01\x00' + struct.pack('<H', len(header)) + header + value)
    workload = {'schemaVersion': 1, 'minimumQuality': .99,
                'model': {'file': source['model']['file'], 'id': source['modelId'], 'sha256': source['model']['sha256']},
                'provenance': {'source': source, 'preparation': 'unchanged uint8 bytes; HWC to batch NHWC framing only',
                               'inputShape': [1] + shape, 'inputDtype': 'uint8'},
                'samples': [{'input': 'input.npy', 'inputSha256': file_digest(root / 'input.npy'),
                             'reference': source['reference']['file'], 'referenceSha256': source['reference']['sha256'],
                             'rtol': .02, 'atol': .0001}]}
    atomic_bytes(root / 'workload.json', canonical(workload))
    native = root / 'native'
    native.mkdir(exist_ok=True)
    code = Path(__file__).resolve().parent
    for name in ('native_yolo_validation.py', 'runtime_service.py', 'common.py'):
        shutil.copyfile(code / name, native / name)
    shutil.copyfile(code.parent / 'business-events/rk_device_tree.py', native / 'rk_device_tree.py')
    return {'workload': str(root / 'workload.json'), 'workloadDigest': file_digest(root / 'workload.json'),
            'modelHash': source['model']['sha256'], 'inputSha256': file_digest(root / 'input.npy'),
            'inputShape': [1] + shape, 'sourceIndependent': True}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--directory', required=True)
    args = parser.parse_args()
    print(json.dumps(prepare(args.directory), sort_keys=True))


if __name__ == '__main__':
    main()
