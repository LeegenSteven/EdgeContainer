"""Typed-final compatibility for the inspected migrated RK stream adapter."""
from __future__ import annotations

import functools
import re
import sys

LANGUAGE = re.compile(r"[A-Za-z]{2,3}(?:-[A-Za-z0-9]{2,8})*\Z")


def unpack_typed_final(raw):
    if raw is None:
        return "", None
    if isinstance(raw, str):
        return raw, None
    if type(raw) is tuple and len(raw) == 2:
        text, language = raw
    elif isinstance(raw, dict):
        text, language = raw.get("text", ""), raw.get("language")
    else:
        raise TypeError("unsupported_rk_asr_final_result")
    if not isinstance(text, str) or (language is not None and (not isinstance(language, str) or not LANGUAGE.fullmatch(language))):
        raise TypeError("invalid_rk_asr_final_types")
    return text, language


def install(module=None):
    if module is None:
        from voxedge.backends.rk import asr as module
    cls, clean = module._RKASRStreamAdapter, module._clean_segment_text
    if hasattr(cls, "_edgestack_original_finalize"):
        return
    original = cls.finalize

    @functools.wraps(original)
    def finalize(stream):
        chunks = stream._chunks
        if chunks and sum(len(chunk) for chunk in chunks) / max(stream._sample_rate, 1) > stream._long_audio_threshold_s:
            # Keep the upstream long-audio segmentation, unload checks, offline
            # transcription and join algorithm exactly as implemented.
            return original(stream)
        raw = stream._live_inner().finalize()
        text, language = unpack_typed_final(raw)
        return clean(text), language

    cls._edgestack_original_finalize, cls.finalize = original, finalize
    sys.stderr.write("edgestack_rk_asr_final_normalization=typed_backend_tuple_or_dict\n")
