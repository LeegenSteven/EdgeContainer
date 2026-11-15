import copy
from pathlib import Path
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from model_receipt import LocalModelInfo, ModelReceiptBridge, observation


def info():
    return {"asr": {"confirmed": True, "loaded": True, "modelFamily": "qwen3-asr", "modelHash": "a" * 64,
                    "lastInferenceModelHash": "a" * 64, "inputWindowSeconds": 4, "lastInferenceWindowSeconds": 4,
                    "successfulEncoderInferences": 3, "lastInferenceAt": "2026-11-15T08:00:00+08:00",
                    "encoderWindows": [{"windowSeconds": 4, "confirmed": True,
                                        "artifacts": [{"sha256": "a" * 64, "runtimeInitialized": True}]}]}}


class Reader:
    def __init__(self, value):
        self.value = value
    def read(self):
        return self.value


class Transport:
    def __init__(self):
        self.requests = []
    def post(self, path, payload):
        self.requests.append((path, payload))
        return {"id": "voice-model-observer"}


class ModelReceiptTests(unittest.TestCase):
    def test_receipt_uses_actual_family_hash_and_original_inference_time(self):
        receipt = observation(info(), slot="asr-encoder", expected_model_id="qwen3-asr")
        self.assertEqual(receipt, {"slot": "asr-encoder", "modelId": "qwen3-asr", "modelHash": "sha256:" + "a" * 64,
                                  "observedAt": "2026-11-15T00:00:00Z", "state": "loaded"})
        with self.assertRaisesRegex(ValueError, "family_mismatch"):
            observation(info(), expected_model_id="desired-but-not-loaded-model")

    def test_unconfirmed_mismatching_or_unused_runtime_never_posts(self):
        cases = [{"confirmed": False}, {"loaded": False}, {"successfulEncoderInferences": 0},
                 {"successfulEncoderInferences": True}, {"modelFamily": ""}, {"modelHash": "desired-label"},
                 {"lastInferenceModelHash": "b" * 64}, {"inputWindowSeconds": 2}, {"lastInferenceWindowSeconds": 2},
                 {"lastInferenceAt": "2026-11-15T00:00:00"}, {"lastInferenceAt": "2999-01-01T00:00:00Z"},
                 {"encoderWindows": []}]
        for changes in cases:
            runtime = info()
            runtime["asr"].update(changes)
            transport = Transport()
            bridge = ModelReceiptBridge(Reader(runtime), transport, "voice-model-observer", "voice-task", "device")
            self.assertEqual(bridge.tick(), "runtime_unconfirmed", changes)
            self.assertEqual(transport.requests, [])

    def test_loaded_artifact_and_window_must_match_actual_inference(self):
        for field, value in (("sha256", "b" * 64), ("runtimeInitialized", False)):
            runtime = info()
            runtime["asr"]["encoderWindows"][0]["artifacts"][0][field] = value
            with self.assertRaisesRegex(ValueError, "artifact_mismatch"):
                observation(runtime)
        for field, value in (("windowSeconds", 2), ("confirmed", False)):
            runtime = info()
            runtime["asr"]["encoderWindows"][0][field] = value
            with self.assertRaisesRegex(ValueError, "window_mismatch"):
                observation(runtime)
        runtime = info()
        runtime["asr"]["encoderWindows"].append(copy.deepcopy(runtime["asr"]["encoderWindows"][0]))
        with self.assertRaisesRegex(ValueError, "single_encoder_required"):
            observation(runtime)

    def test_bridge_posts_only_observation_to_source_scoped_status(self):
        transport = Transport()
        bridge = ModelReceiptBridge(Reader(info()), transport, "voice-model-observer", "voice-task", "rk3588")
        self.assertEqual(bridge.tick(), "receipt_submitted")
        path, payload = transport.requests[0]
        self.assertEqual(path, "/api/edgestack/ingest/status")
        self.assertEqual(payload["sourceId"], "voice-model-observer")
        self.assertEqual(payload["taskId"], "voice-task")
        self.assertEqual(payload["deviceId"], "rk3588")
        self.assertEqual(payload["observations"][0]["modelId"], "qwen3-asr")
        self.assertNotIn("checkpoint", payload)
        self.assertNotIn("coverage", payload)

    def test_runtime_and_server_failures_do_not_manufacture_receipts(self):
        class BrokenReader:
            def read(self):
                raise OSError("unavailable")
        transport = Transport()
        self.assertEqual(ModelReceiptBridge(BrokenReader(), transport, "s", "t", "d").tick(), "runtime_unavailable")
        self.assertEqual(transport.requests, [])
        class BrokenTransport:
            def post(self, *_):
                raise OSError("offline")
        self.assertEqual(ModelReceiptBridge(Reader(info()), BrokenTransport(), "s", "t", "d").tick(), "receipt_delivery_failed")

    def test_local_reader_rejects_non_loopback_and_redirectable_identity_urls(self):
        for url in ("http://192.0.2.20:18621/model-info", "http://127.0.0.1:18621/model-info?token=x",
                    "http://user@127.0.0.1/model-info", "http://127.0.0.1/other", "https://127.0.0.1/model-info"):
            with self.assertRaisesRegex(ValueError, "loopback_url"):
                LocalModelInfo("unused-secret-file", url)

    def test_missing_or_empty_local_secret_fails_before_network(self):
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / "voice-token"
            reader = LocalModelInfo(path)
            class NoNetwork:
                def open(self, *_args, **_kwargs):
                    raise AssertionError("network must not be called")
            reader.opener = NoNetwork()
            with self.assertRaises(OSError):
                reader.read()
            path.write_text("\n")
            with self.assertRaisesRegex(ValueError, "credential"):
                reader.read()


if __name__ == "__main__":
    unittest.main()
