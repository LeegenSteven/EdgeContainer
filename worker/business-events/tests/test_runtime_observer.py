import hashlib
from pathlib import Path
from types import SimpleNamespace
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from runtime_observer import RuntimeObserver


class RuntimeObserverTests(unittest.TestCase):
    def test_deployed_release_only_decoder_cleanup_invalidates_receipt(self):
        class Decoder:
            def __init__(self, model_path):
                pass
            def release(self):
                return "released"
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / "test-decoder.rkllm"
            path.write_bytes(b"synthetic deployed decoder artifact")
            observer = RuntimeObserver(lambda: None)
            observer.instrument_decoder(Decoder)
            decoder = Decoder(path)
            self.assertTrue(observer.records[id(decoder)]["runtimeInitialized"])
            self.assertEqual(decoder.release(), "released")
            self.assertNotIn(id(decoder), observer.records)

    def test_only_successful_loaded_current_runtime_and_inference_are_confirmed(self):
        class Native:
            def load_rknn(self, path):
                return 0
            def init_runtime(self):
                return 0
            def release(self):
                return None
        class Decoder:
            def __init__(self, model_path):
                pass
            def close(self):
                pass
        class Encoder:
            def encode(self, samples):
                return ("hidden", 4.0, 2)
        class Qwen3ASREngine:
            pass
        Qwen3ASREngine.__module__ = "rkvoice_stream.backends.asr.qwen3.engine"
        class Qwen3ASRRKBackend:
            def is_ready(self):
                return True
        Qwen3ASRRKBackend.__module__ = "rkvoice_stream.backends.asr.qwen3_rk"
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / "test-encoder.rknn"
            path.write_bytes(b"synthetic test artifact")
            decoder_path = Path(temp) / "test-decoder.rkllm"
            decoder_path.write_bytes(b"synthetic decoder artifact")
            backend = SimpleNamespace(_inner=None)
            observer = RuntimeObserver(lambda: backend)
            observer.instrument_rknn(Native)
            observer.instrument_decoder(Decoder)
            observer.instrument_encoder(Encoder)
            self.assertFalse(observer.observe()["loaded"])
            native = Native()
            native.load_rknn(path)
            encoder = Encoder()
            encoder._mode = "merged"
            encoder._models = {2: (native, 200, 26)}
            engine = Qwen3ASREngine()
            engine.encoder, engine.decoder = encoder, Decoder(decoder_path)
            backend._inner = Qwen3ASRRKBackend()
            backend._inner._engine = engine
            self.assertFalse(observer.observe()["confirmed"])
            native.init_runtime()
            loaded = observer.observe()
            self.assertTrue(loaded["confirmed"])
            self.assertEqual(loaded["modelHash"], hashlib.sha256(path.read_bytes()).hexdigest())
            self.assertIsNone(loaded["inputWindowSeconds"])
            encoder.encode("fixture samples")
            inferred = observer.observe()
            self.assertEqual(inferred["inputWindowSeconds"], 2)
            self.assertEqual(inferred["successfulEncoderInferences"], 1)
            self.assertEqual(inferred["lastInferenceModelHash"], loaded["modelHash"])
            self.assertEqual(inferred["modelFamily"], "qwen3-asr")
            actual_inner = backend._inner
            backend._inner = SimpleNamespace(_engine=engine, is_ready=lambda: True)
            unknown = observer.observe()
            self.assertFalse(unknown["confirmed"])
            self.assertNotIn("modelFamily", unknown)
            backend._inner = actual_inner
            native.release()
            self.assertFalse(observer.observe()["confirmed"])


if __name__ == "__main__":
    unittest.main()
