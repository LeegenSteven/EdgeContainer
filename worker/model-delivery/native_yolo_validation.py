#!/usr/bin/env python3
"""Run one bounded frozen RKNN workload; the caller must hold the board lease."""
import argparse
import json
import math
from pathlib import Path
import platform
import statistics
import sys
import time

from common import DeliveryError, file_digest
from runtime_service import YoloRuntime


def validate(workload_file, device_tree_file=None, runtime_factory=YoloRuntime):
    if sys.platform != 'linux' or platform.machine() not in ('aarch64', 'arm64'):
        raise DeliveryError('native_platform_mismatch', 'Native RKNN validation requires the actual ARM64 Linux board')
    path = Path(workload_file).resolve()
    workload = json.loads(path.read_text())
    model = workload['model']
    model_path = (path.parent / model['file']).resolve()
    minimum = workload.get('minimumQuality')
    if (not model_path.is_relative_to(path.parent) or file_digest(model_path) != model['sha256']
            or not 1 <= len(workload.get('samples', [])) <= 32
            or type(minimum) not in (int, float) or not math.isfinite(minimum) or not 0 <= minimum <= 1):
        raise DeliveryError('native_workload_invalid', 'Use an exact model and one to 32 frozen samples')
    runtime = runtime_factory({'modelPath': str(model_path), 'modelId': model['id'], 'deviceTreeFile': device_tree_file})
    try:
        runtime.start()
        qualities, durations = [], []
        for sample in workload['samples']:
            started = time.monotonic()
            quality = runtime.infer(sample, path.parent)
            durations.append((time.monotonic() - started) * 1000)
            if type(quality) not in (int, float) or not math.isfinite(quality) or not 0 <= quality <= 1:
                raise DeliveryError('native_output_invalid', 'RKNN quality is not a finite measured ratio')
            qualities.append(quality)
        evidence = runtime.observe()
        quality = statistics.mean(qualities)
        if (evidence['modelHash'] != model['sha256'] or evidence['modelId'] != model['id']
                or quality < minimum):
            raise DeliveryError('native_validation_failed', 'Real loaded-model inference failed the frozen reference gate')
        return {'status': 'succeeded', 'simulated': False, 'modelId': evidence['modelId'],
                'modelHash': evidence['modelHash'], 'workloadDigest': file_digest(path),
                'samples': len(qualities), 'latencyMs': statistics.mean(durations),
                'quality': quality, 'errorRate': 0, 'memoryBytes': evidence['memoryBytes'],
                'observedAt': evidence['observedAt']}
    finally:
        runtime.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--workload', required=True)
    parser.add_argument('--device-tree-file')
    args = parser.parse_args()
    try:
        print(json.dumps(validate(args.workload, args.device_tree_file), allow_nan=False))
    except (DeliveryError, OSError, ValueError, KeyError) as error:
        print(json.dumps({'errorCode': getattr(error, 'code', 'native_validation_unavailable')}), file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
