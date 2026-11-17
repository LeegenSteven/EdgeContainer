#!/usr/bin/env python3
"""Call a fixed administrator-configured inference validation endpoint."""
from __future__ import annotations

import argparse
import json
from pathlib import Path
import sys
import time

from common import ApiError, DeliveryError, JsonClient, TransportError


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--config', required=True)
    for key in ('device-id', 'task-id', 'image', 'model-hash', 'runtime-profile-digest', 'nonce'):
        parser.add_argument('--' + key, required=True)
    arguments = parser.parse_args(argv)
    config = json.loads(Path(arguments.config).read_text())
    client = JsonClient(**config['service'])
    path = config['path']
    if not path.startswith('/') or path.startswith('//') or '?' in path:
        raise DeliveryError('invalid_probe_path', 'Validation path must be administrator configured')
    inputs = json.loads(Path(config['workloadFile']).read_text())
    request = {'deviceId': arguments.device_id, 'taskId': arguments.task_id, 'image': arguments.image,
               'modelHash': arguments.model_hash, 'runtimeProfileDigest': arguments.runtime_profile_digest,
               'nonce': arguments.nonce, 'inputs': inputs}
    deadline = time.monotonic() + config.get('startupTimeoutSeconds', 300)
    while True:
        try:
            proof = client.request(path, request)
            # The actual runtime supplies identities and measurements. The worker verifies their binding.
            print(json.dumps(proof, allow_nan=False))
            return
        except (ApiError, TransportError) as error:
            if isinstance(error, ApiError) and error.status not in (409, 425, 502, 503):
                raise
            if time.monotonic() >= deadline:
                raise DeliveryError('container_probe_timeout', 'The deployed inference service did not become ready') from error
            # Validation calls must be read-only tests using a nonce idempotency key on the runtime.
            time.sleep(3)


if __name__ == '__main__':
    try:
        main()
    except (DeliveryError, OSError, ValueError, KeyError) as error:
        print(json.dumps({'errorCode': getattr(error, 'code', 'probe_configuration_invalid')}), file=sys.stderr)
        sys.exit(1)
