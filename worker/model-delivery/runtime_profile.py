#!/usr/bin/env python3
"""Freeze installed base-runtime files on the actual target architecture."""
import argparse
import platform
from pathlib import Path

from common import DeliveryError, atomic_bytes, canonical, file_digest
from runtime_service import measured_profile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', required=True)
    parser.add_argument('--runtime-config', required=True)
    parser.add_argument('--workload', required=True)
    parser.add_argument('--backend', required=True, choices=('rknn-asr', 'rknn-yolo'))
    parser.add_argument('--file', action='append', default=[], metavar='NAME=ABSOLUTE_PATH')
    args = parser.parse_args()
    entries = [('runtime-config', args.runtime_config), ('workload-manifest', args.workload)]
    for entry in args.file:
        name, separator, path = entry.partition('=')
        if not separator or not name or not Path(path).is_absolute():
            raise DeliveryError('runtime_profile_invalid', 'Profile files require NAME=ABSOLUTE_PATH')
        entries.append((name, path))
    if not args.file:
        raise DeliveryError('runtime_profile_invalid', 'Profile the installed inference code and native libraries')
    architecture = {'aarch64': 'arm64', 'x86_64': 'amd64'}.get(platform.machine(), platform.machine())
    files = [{'name': name, 'path': str(Path(path).resolve()), 'sha256': file_digest(path)}
             for name, path in entries]
    atomic_bytes(args.output, canonical({'schemaVersion': 1, 'architecture': architecture,
                                        'backend': args.backend, 'files': files}))
    print(measured_profile(args.output))


if __name__ == '__main__':
    main()
