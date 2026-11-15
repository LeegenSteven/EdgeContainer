#!/usr/bin/env python3
"""Forward verified local runtime observations; never initiates model changes."""
from __future__ import annotations

import argparse
import datetime as dt
import json
import os
from pathlib import Path
import signal
import threading
import time
import urllib.parse
import urllib.request

from adapter import HTTPTransport, NoRedirect


def digest(value):
    if not isinstance(value, str) or len(value) != 64 or any(character not in "0123456789abcdefABCDEF" for character in value):
        raise ValueError("invalid_runtime_hash")
    return value.lower()


def observation(info, slot="asr-encoder", expected_model_id=None):
    """Identity comes exclusively from authenticated current runtime evidence."""
    asr = info.get("asr", {}) if isinstance(info, dict) else {}
    if asr.get("confirmed") is not True or asr.get("loaded") is not True:
        raise ValueError("runtime_identity_unconfirmed")
    if type(asr.get("successfulEncoderInferences")) is not int or asr["successfulEncoderInferences"] < 1:
        raise ValueError("runtime_inference_unconfirmed")
    model_id = asr.get("modelFamily")
    if not isinstance(model_id, str) or not model_id or len(model_id) > 200:
        raise ValueError("runtime_model_family_missing")
    if expected_model_id and model_id != expected_model_id:
        raise ValueError("runtime_model_family_mismatch")
    model_hash = digest(asr.get("modelHash"))
    if digest(asr.get("lastInferenceModelHash")) != model_hash:
        raise ValueError("runtime_inference_hash_mismatch")
    window = asr.get("inputWindowSeconds")
    if type(window) is not int or window < 1 or asr.get("lastInferenceWindowSeconds") != window:
        raise ValueError("runtime_inference_window_mismatch")
    windows = asr.get("encoderWindows")
    if not isinstance(windows, list) or len(windows) != 1 or not isinstance(windows[0], dict):
        raise ValueError("runtime_single_encoder_required")
    loaded = windows[0]
    artifacts = loaded.get("artifacts")
    if loaded.get("windowSeconds") != window or loaded.get("confirmed") is not True or not isinstance(artifacts, list) or len(artifacts) != 1 or not isinstance(artifacts[0], dict):
        raise ValueError("runtime_loaded_window_mismatch")
    if artifacts[0].get("runtimeInitialized") is not True or digest(artifacts[0].get("sha256")) != model_hash:
        raise ValueError("runtime_loaded_artifact_mismatch")
    try:
        timestamp = dt.datetime.fromisoformat(asr["lastInferenceAt"].replace("Z", "+00:00"))
        if timestamp.tzinfo is None or timestamp > dt.datetime.now(dt.timezone.utc) + dt.timedelta(minutes=5):
            raise ValueError()
    except (KeyError, TypeError, AttributeError, ValueError):
        raise ValueError("runtime_inference_time_invalid") from None
    return {"slot": slot, "modelId": model_id, "modelHash": "sha256:" + model_hash,
            "observedAt": timestamp.astimezone(dt.timezone.utc).isoformat().replace("+00:00", "Z"), "state": "loaded"}


class LocalModelInfo:
    def __init__(self, token_file, url="http://127.0.0.1:18621/model-info", timeout=8):
        parsed = urllib.parse.urlsplit(url)
        if parsed.scheme != "http" or parsed.hostname not in {"127.0.0.1", "::1", "localhost"} or parsed.path != "/model-info" or parsed.username or parsed.password or parsed.query or parsed.fragment:
            raise ValueError("model_info_requires_authenticated_loopback_url")
        self.url, self.token_file, self.timeout = url, Path(token_file), timeout
        # No proxy or redirect can move the Worker-only secret off loopback.
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())

    def read(self):
        with self.token_file.open("rb") as stream:
            token = stream.read(4097).strip()
        if not token or len(token) > 4096 or b"\n" in token or b"\r" in token:
            raise ValueError("invalid_voice_upstream_credential")
        request = urllib.request.Request(self.url, headers={"Authorization": "Bearer " + token.decode("ascii")})
        with self.opener.open(request, timeout=self.timeout) as response:
            payload = response.read(1024 * 1024 + 1)
            if len(payload) > 1024 * 1024:
                raise ValueError("model_info_response_too_large")
            return json.loads(payload)


class ModelReceiptBridge:
    def __init__(self, reader, transport, source_id, task_id, device_id, slot="asr-encoder", expected_model_id=None):
        self.reader, self.transport = reader, transport
        self.source_id, self.task_id, self.device_id = source_id, task_id, device_id
        self.slot, self.expected_model_id = slot, expected_model_id

    def tick(self):
        try:
            info = self.reader.read()
        except Exception:
            return "runtime_unavailable"
        try:
            receipt = observation(info, self.slot, self.expected_model_id)
        except (ValueError, TypeError, AttributeError):
            return "runtime_unconfirmed"
        try:
            self.transport.post("/api/edgestack/ingest/status", {"sourceId": self.source_id, "taskId": self.task_id,
                                "deviceId": self.device_id, "status": "healthy", "observations": [receipt]})
            return "receipt_submitted"
        except Exception:
            return "receipt_delivery_failed"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--once", action="store_true")
    args = parser.parse_args()
    reader = LocalModelInfo(os.environ["VOICE_UPSTREAM_TOKEN_FILE"], os.environ.get("MODEL_INFO_URL", "http://127.0.0.1:18621/model-info"))
    transport = HTTPTransport(os.environ["INGEST_BASE_URL"], os.environ.get("MODEL_OBSERVER_TOKEN_FILE", "/run/secrets/model_observer_token"),
                              ca_file=os.environ.get("INGEST_CA_FILE"), cert_sha256=os.environ.get("INGEST_CERT_SHA256"))
    bridge = ModelReceiptBridge(reader, transport, os.environ["EVENT_SOURCE_ID"], os.environ["EVENT_TASK_ID"], os.environ["EVENT_DEVICE_ID"],
                                slot=os.environ.get("MODEL_OBSERVER_SLOT", "asr-encoder"), expected_model_id=os.environ.get("MODEL_OBSERVER_EXPECTED_MODEL_ID"))
    stop = threading.Event()
    signal.signal(signal.SIGTERM, lambda *_: stop.set())
    signal.signal(signal.SIGINT, lambda *_: stop.set())
    previous = None
    while not stop.is_set():
        started = time.monotonic()
        result = bridge.tick()
        if result != previous or args.once:
            print("model_observer_" + result, flush=True)
            previous = result
        if args.once:
            return 0 if result == "receipt_submitted" else 1
        stop.wait(max(0, 20 - (time.monotonic() - started)))


if __name__ == "__main__":
    raise SystemExit(main())
