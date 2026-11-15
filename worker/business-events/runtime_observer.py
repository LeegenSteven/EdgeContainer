"""Read-only load/inference receipts for the inspected Qwen3 RK runtime.

Instrumentation calls original functions unchanged. Model bytes are hashed only
inside Worker; no weight data or environment configuration is exported.
"""
from __future__ import annotations

import functools
import hashlib
from pathlib import Path
import threading

from adapter import now


class RuntimeObserver:
    def __init__(self, backend_getter):
        self.backend_getter = backend_getter
        self.records = {}
        self.lock = threading.Lock()

    @staticmethod
    def fingerprint(path):
        path = Path(path).resolve(strict=True)
        before = path.stat()
        digest = hashlib.sha256()
        with path.open("rb") as stream:
            while True:
                data = stream.read(1024 * 1024)
                if not data:
                    break
                digest.update(data)
        after = path.stat()
        if (before.st_ino, before.st_size, before.st_mtime_ns) != (after.st_ino, after.st_size, after.st_mtime_ns):
            raise ValueError("artifact_changed_during_hash")
        return {"path": str(path), "sha256": digest.hexdigest(), "sizeBytes": after.st_size, "loadedAt": now()}

    def record(self, instance, path, *, initialized=False):
        try:
            receipt = self.fingerprint(path)
        except Exception:
            receipt = {"confirmed": False, "reason": "loaded_artifact_hash_unavailable"}
        with self.lock:
            self.records[id(instance)] = {**receipt, "runtimeInitialized": initialized}

    def instrument_rknn(self, cls):
        observer = self
        original_load, original_init, original_release = cls.load_rknn, cls.init_runtime, cls.release

        @functools.wraps(original_load)
        def load(instance, path, *args, **kwargs):
            result = original_load(instance, path, *args, **kwargs)
            if result == 0:
                observer.record(instance, path)
            else:
                with observer.lock:
                    observer.records.pop(id(instance), None)
            return result

        @functools.wraps(original_init)
        def initialize(instance, *args, **kwargs):
            result = original_init(instance, *args, **kwargs)
            with observer.lock:
                receipt = observer.records.get(id(instance))
                if receipt is not None:
                    receipt["runtimeInitialized"] = result == 0
            return result

        @functools.wraps(original_release)
        def release(instance, *args, **kwargs):
            try:
                return original_release(instance, *args, **kwargs)
            finally:
                with observer.lock:
                    observer.records.pop(id(instance), None)
        cls.load_rknn, cls.init_runtime, cls.release = load, initialize, release

    def instrument_decoder(self, cls):
        observer = self
        original_init = cls.__init__
        # The deployed RKLLMDecoder exposes release(); other versions use close().
        cleanup = {name: getattr(cls, name) for name in ("release", "close") if callable(getattr(cls, name, None))}
        if not cleanup:
            raise TypeError("runtime_observer_missing_decoder_cleanup_api")

        @functools.wraps(original_init)
        def initialize(instance, model_path, *args, **kwargs):
            original_init(instance, model_path, *args, **kwargs)
            observer.record(instance, model_path, initialized=True)

        def cleanup_wrapper(original):
            @functools.wraps(original)
            def release(instance, *args, **kwargs):
                try:
                    return original(instance, *args, **kwargs)
                finally:
                    with observer.lock:
                        observer.records.pop(id(instance), None)
            return release
        cls.__init__ = initialize
        for name, original in cleanup.items():
            setattr(cls, name, cleanup_wrapper(original))

    @staticmethod
    def instrument_encoder(cls):
        original_encode = cls.encode

        @functools.wraps(original_encode)
        def encode(instance, *args, **kwargs):
            result = original_encode(instance, *args, **kwargs)
            instance._edgestack_last_window = result[2]
            instance._edgestack_inference_count = getattr(instance, "_edgestack_inference_count", 0) + 1
            instance._edgestack_last_inference_at = now()
            return result
        cls.encode = encode

    def observe(self):
        backend = self.backend_getter()
        inner = getattr(backend, "_inner", None)
        engine = getattr(inner, "_engine", None)
        encoder = getattr(engine, "encoder", None)
        models = getattr(encoder, "_models", {})
        if not engine or not models or not inner.is_ready():
            return {"confirmed": False, "loaded": False, "reason": "asr_runtime_not_ready"}
        inner_type = type(inner).__module__ + "." + type(inner).__name__
        engine_type = type(engine).__module__ + "." + type(engine).__name__
        if inner_type != "rkvoice_stream.backends.asr.qwen3_rk.Qwen3ASRRKBackend" or engine_type != "rkvoice_stream.backends.asr.qwen3.engine.Qwen3ASREngine":
            return {"confirmed": False, "loaded": True, "reason": "unsupported_runtime_identity", "backendType": inner_type, "engineType": engine_type}
        windows, all_confirmed = [], True
        with self.lock:
            for seconds, model in sorted(models.items()):
                native_models = model[:1] if getattr(encoder, "_mode", None) == "merged" else model[:2]
                artifacts = [dict(self.records.get(id(native), {})) for native in native_models]
                confirmed = all(artifact.get("runtimeInitialized") and artifact.get("sha256") for artifact in artifacts)
                all_confirmed = all_confirmed and confirmed
                windows.append({"windowSeconds": seconds, "confirmed": bool(confirmed), "artifacts": artifacts})
            decoder = dict(self.records.get(id(getattr(engine, "decoder", None)), {}))
        decoder_confirmed = bool(decoder.get("runtimeInitialized") and decoder.get("sha256"))
        last_window = getattr(encoder, "_edgestack_last_window", None)
        loaded_hash = windows[0]["artifacts"][0].get("sha256") if len(windows) == 1 and len(windows[0]["artifacts"]) == 1 else None
        last_hash = next((window["artifacts"][0].get("sha256") for window in windows if window["windowSeconds"] == last_window and len(window["artifacts"]) == 1), None)
        return {"confirmed": bool(all_confirmed and decoder_confirmed), "loaded": True, "modelFamily": "qwen3-asr",
                "backendType": inner_type, "engineType": engine_type,
                "identityBasis": "successful_native_load_and_runtime_init", "encoderWindows": windows,
                "modelHash": loaded_hash, "inputWindowSeconds": last_window, "lastInferenceModelHash": last_hash,
                "decoder": {"confirmed": decoder_confirmed, **decoder},
                "lastInferenceWindowSeconds": last_window,
                "successfulEncoderInferences": getattr(encoder, "_edgestack_inference_count", 0),
                "lastInferenceAt": getattr(encoder, "_edgestack_last_inference_at", None)}


def install(backend_getter):
    """Install before upstream lifespan startup loads model runtimes."""
    from rknnlite.api import RKNNLite
    from rkvoice_stream.backends.asr.qwen3.decoder import RKLLMDecoder
    from rkvoice_stream.backends.asr.qwen3.encoder import RknnEncoder
    # Validate the complete API before patching anything; unsupported versions
    # remain unconfirmed without partially installed instrumentation.
    if not all(callable(getattr(RKNNLite, name, None)) for name in ("load_rknn", "init_runtime", "release")):
        raise TypeError("runtime_observer_missing_rknn_api")
    if not any(callable(getattr(RKLLMDecoder, name, None)) for name in ("release", "close")):
        raise TypeError("runtime_observer_missing_decoder_cleanup_api")
    if not callable(getattr(RknnEncoder, "encode", None)):
        raise TypeError("runtime_observer_missing_encoder_api")
    observer = RuntimeObserver(backend_getter)
    observer.instrument_rknn(RKNNLite)
    observer.instrument_decoder(RKLLMDecoder)
    observer.instrument_encoder(RknnEncoder)
    return observer.observe
